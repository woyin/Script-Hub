package manager

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const backupLimit int64 = 64 << 20
const backupCount = 5

type BackupInfo struct {
	Name      string    `json:"name"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) backupDir() string { return filepath.Join(s.root, "backups") }
func (s *Store) backups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(s.backupDir())
	if os.IsNotExist(err) {
		return []BackupInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []BackupInfo{}
	for _, e := range entries {
		if e.IsDir() || !validBackup(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, BackupInfo{e.Name(), info.Size(), info.ModTime().UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

var backupName = regexp.MustCompile(`^snapshot-[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[a-f0-9]{8}\.tar\.gz$`)

func validBackup(name string) bool { return backupName.MatchString(name) }

func (s *Store) Backups() ([]BackupInfo, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	return s.backups()
}

type snapshotWriter struct {
	store   *Store
	file    *os.File
	used    int64
	pending int64
	old     []BackupInfo
}

func (w *snapshotWriter) Write(p []byte) (int, error) {
	for w.used+w.pending+int64(len(p)) > backupLimit {
		if len(w.old) <= 1 {
			return 0, ErrLimit
		}
		index := -1
		for i, old := range w.old {
			if w.store.backupPins[old.Name] == 0 {
				index = i
				break
			}
		}
		if index < 0 {
			return 0, ErrLimit
		}
		old := w.old[index]
		if err := os.Remove(filepath.Join(w.store.backupDir(), old.Name)); err != nil {
			return 0, err
		}
		w.used -= old.Bytes
		w.old = append(w.old[:index], w.old[index+1:]...)

	}
	n, err := w.file.Write(p)
	w.pending += int64(n)
	return n, err
}
func (s *Store) Snapshot() (BackupInfo, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}
func (s *Store) snapshotLocked() (BackupInfo, error) { return s.snapshotAtLocked(time.Now().UTC()) }
func (s *Store) snapshotAtLocked(checkedAt time.Time) (BackupInfo, error) {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return BackupInfo{}, err
	}
	old, err := s.backups()
	if err != nil {
		return BackupInfo{}, err
	}
	var used int64
	for _, b := range old {
		used += b.Bytes
	}
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return BackupInfo{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	writer := &snapshotWriter{store: s, file: f, used: used, old: old}
	gz := gzip.NewWriter(writer)
	tw := tar.NewWriter(gz)
	manifest, _ := json.Marshal(map[string]any{"format": 1, "createdAt": time.Now().UTC(), "fingerprint": s.fingerprintLocked()})
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(manifest))}); err != nil {
		return BackupInfo{}, err
	}
	if _, err = tw.Write(manifest); err != nil {
		return BackupInfo{}, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return BackupInfo{}, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if !resourceID.MatchString(strings.TrimSuffix(e.Name(), ".json")) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return BackupInfo{}, err
		}
		if err = tw.WriteHeader(&tar.Header{Name: e.Name(), Mode: 0600, Size: info.Size()}); err != nil {
			return BackupInfo{}, err
		}
		in, err := os.Open(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return BackupInfo{}, err
		}
		_, err = io.Copy(tw, in)
		in.Close()
		if err != nil {
			return BackupInfo{}, err
		}
	}
	if err = tw.Close(); err != nil {
		return BackupInfo{}, err
	}
	if err = gz.Close(); err != nil {
		return BackupInfo{}, err
	}
	if err = f.Sync(); err != nil {
		return BackupInfo{}, err
	}
	if err = f.Close(); err != nil {
		return BackupInfo{}, err
	}
	token, err := randomToken()
	if err != nil {
		return BackupInfo{}, err
	}
	name := "snapshot-" + time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + token[:8] + ".tar.gz"
	retained, err := s.backups()
	if err != nil {
		return BackupInfo{}, err
	}
	if len(retained) >= backupCount {
		eligible := false
		for _, b := range retained {
			if s.backupPins[b.Name] == 0 {
				eligible = true
				break
			}
		}
		if !eligible {
			return BackupInfo{}, ErrLimit
		}
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return BackupInfo{}, err
	}
	old, err = s.backups()
	if err != nil {
		return BackupInfo{}, err
	}
	for len(old) > backupCount {
		index := -1
		for i, b := range old {
			if b.Name != name && s.backupPins[b.Name] == 0 {
				index = i
				break
			}
		}
		if index < 0 {
			return BackupInfo{}, ErrLimit
		}
		if err = os.Remove(filepath.Join(dir, old[index].Name)); err != nil {
			return BackupInfo{}, err
		}
		old = append(old[:index], old[index+1:]...)
	}

	if err = s.saveCheckpoint(backupCheckpoint{Fingerprint: s.fingerprintLocked(), Snapshot: name, CheckedAt: checkedAt}); err != nil {
		return BackupInfo{}, err
	}
	return BackupInfo{name, writer.pending, time.Now().UTC()}, nil
}

// Restore validates a whole generation before atomically publishing its pointer.
// Resource IDs and share digests survive, but all edit/cache revisions change.
func (s *Store) Restore(reader io.Reader) (int, error) {
	token, err := randomToken()
	if err != nil {
		return 0, err
	}
	name := "records-" + token
	stage := filepath.Join(s.root, name)
	if err = os.Mkdir(stage, 0700); err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(stage)
		}
	}()
	gz, err := gzip.NewReader(io.LimitReader(reader, backupLimit+1))
	if err != nil {
		return 0, fmt.Errorf("%w: invalid gzip backup", ErrInvalid)
	}
	defer gz.Close()
	decoded := &budgetReader{src: gz, left: s.maxBytes + (1 << 20)}
	tr := tar.NewReader(decoded)
	seen := map[string]bool{}
	restoredIndex := map[string]resourceMeta{}
	count := 0
	manifest := false
	var used int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("%w: invalid backup archive", ErrInvalid)
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > MaxDocumentBytes {
			return 0, ErrInvalid
		}
		if header.Name == "manifest.json" {
			if manifest {
				return 0, ErrInvalid
			}
			var m struct {
				Format    int       `json:"format"`
				CreatedAt time.Time `json:"createdAt"`
			}
			if header.Size > 4096 || json.NewDecoder(tr).Decode(&m) != nil || m.Format != 1 {
				return 0, ErrInvalid
			}
			manifest = true
			continue
		}
		id := strings.TrimSuffix(header.Name, ".json")
		if header.Name != id+".json" || !resourceID.MatchString(id) || seen[id] {
			return 0, ErrInvalid
		}
		seen[id] = true
		count++
		if count > MaxResources {
			return 0, ErrLimit
		}
		var r Record
		decoder := json.NewDecoder(io.LimitReader(tr, MaxDocumentBytes+1))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&r); err != nil {
			return 0, ErrInvalid
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return 0, ErrInvalid
		}
		if r.ID != id || ValidateSpec(r.Kind, r.Spec) != nil {
			return 0, ErrInvalid
		}
		if r.ShareDigest != "" && (len(r.ShareDigest) != 64 || strings.Trim(r.ShareDigest, "0123456789abcdef") != "") {
			return 0, ErrInvalid
		}
		r.UpdatedAt = time.Now().UTC()
		restoredIndex[r.ID] = metadataFor(r)
		b, err := json.Marshal(r)
		if err != nil {
			return 0, err
		}
		if len(b) > MaxDocumentBytes {
			return 0, ErrLimit
		}
		used += int64(len(b))
		if used > s.maxBytes {
			return 0, ErrLimit
		}
		if err = os.WriteFile(filepath.Join(stage, id+".json"), b, 0600); err != nil {
			return 0, err
		}
	}
	// Consume gzip's footer to reject truncated streams and checksum failures before switching.
	if _, err = io.Copy(io.Discard, decoded); err != nil {
		return 0, ErrInvalid
	}
	if !manifest {
		return 0, ErrInvalid
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.snapshotLocked(); err != nil {
		return 0, errors.New("cannot create safety backup; restore cancelled")
	}
	if err = s.commitGenerationLocked(stage, restoredIndex); err != nil {
		return 0, err
	}
	committed = true
	return count, nil
}

// commitGenerationLocked publishes complete resource sets through one durable
// pointer. Restore and Sub-Store collection replacement share this commit path.
// The caller holds Store.mu and Store.backupMu and owns stage on failure.
func (s *Store) commitGenerationLocked(stage string, index map[string]resourceMeta) error {
	name := filepath.Base(stage)
	pointer, err := os.CreateTemp(s.root, ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(pointer.Name())
	defer pointer.Close()
	if _, err = pointer.WriteString(name); err != nil {
		return err
	}
	if err = pointer.Sync(); err != nil {
		return err
	}
	if err = pointer.Close(); err != nil {
		return err
	}
	// Flush staged files before committing the generation pointer.
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, e := range entries {
		f, err := os.OpenFile(filepath.Join(stage, e.Name()), os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	directory, err := os.Open(stage)
	if err != nil {
		return err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return err
	}
	if err = os.Rename(pointer.Name(), filepath.Join(s.root, ".active")); err != nil {
		return err
	}
	old := s.dir
	s.dir = stage
	s.index = index
	s.notify()
	if directory, e := os.Open(s.root); e == nil {
		_ = directory.Sync()
		directory.Close()
	}
	if old != s.root {
		os.RemoveAll(old)
	} else {
		entries, _ := os.ReadDir(old)
		for _, e := range entries {
			if resourceID.MatchString(strings.TrimSuffix(e.Name(), ".json")) && strings.HasSuffix(e.Name(), ".json") {
				os.Remove(filepath.Join(old, e.Name()))
			}
		}
	}
	return nil
}

type budgetReader struct {
	src  io.Reader
	left int64
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, ErrLimit
	}
	if int64(len(p)) > r.left {
		p = p[:r.left]
	}
	n, err := r.src.Read(p)
	r.left -= int64(n)
	return n, err
}

func (s *Store) openBackup(name string) (*os.File, func(), error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	f, err := os.Open(filepath.Join(s.backupDir(), name))
	if err != nil {
		return nil, func() {}, err
	}
	s.backupPins[name]++
	return f, func() {
		f.Close()
		s.backupMu.Lock()
		s.backupPins[name]--
		if s.backupPins[name] == 0 {
			delete(s.backupPins, name)
		}
		s.backupMu.Unlock()
	}, nil
}
