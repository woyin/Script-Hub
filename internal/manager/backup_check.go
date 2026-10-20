package manager

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type backupCheckpoint struct {
	Fingerprint string    `json:"fingerprint"`
	Snapshot    string    `json:"snapshot"`
	CheckedAt   time.Time `json:"checkedAt"`
}
type BackupStatus struct {
	CheckedAt      time.Time `json:"checkedAt"`
	LastSnapshotAt time.Time `json:"lastSnapshotAt"`
}

func (s *Store) checkpointPath() string { return filepath.Join(s.root, "runtime", "backup-state.json") }
func (s *Store) checkpoint() backupCheckpoint {
	f, err := os.Open(s.checkpointPath())
	if err != nil {
		return backupCheckpoint{}
	}
	defer f.Close()
	var c backupCheckpoint
	if json.NewDecoder(io.LimitReader(f, 4096)).Decode(&c) != nil {
		return backupCheckpoint{}
	}
	return c
}
func (s *Store) fingerprintLocked() string {
	ids := make([]string, 0, len(s.index))
	for id := range s.index {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var data strings.Builder
	for _, id := range ids {
		data.WriteString(id)
		data.WriteString(s.index[id].Digest)
	}
	return digest(data.String())
}
func (s *Store) saveCheckpoint(c backupCheckpoint) error {
	if err := os.MkdirAll(filepath.Dir(s.checkpointPath()), 0700); err != nil {
		return err
	}
	return atomicJSON(s.checkpointPath(), c)
}

// SnapshotIfChanged compresses only changed resource data and permissions. Manual
// snapshots and the pre-restore safety snapshot still always create a new file.
func (s *Store) SnapshotIfChanged(now time.Time) (bool, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	old, err := s.backups()
	if err != nil {
		return false, err
	}
	c := s.checkpoint()
	same := c.Fingerprint == s.fingerprintLocked()
	exists := false
	for _, b := range old {
		if b.Name == c.Snapshot {
			exists = true
			break
		}
	}
	if same && exists || len(s.index) == 0 && len(old) == 0 {
		c.Fingerprint = s.fingerprintLocked()
		c.CheckedAt = now
		return false, s.saveCheckpoint(c)
	}
	_, err = s.snapshotAtLocked(now)
	return err == nil, err
}
func (s *Store) BackupStatus() (BackupStatus, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	c := s.checkpoint()
	out := BackupStatus{CheckedAt: c.CheckedAt}
	rows, err := s.backups()
	if err != nil {
		return out, err
	}
	for _, b := range rows {
		if b.Name == c.Snapshot {
			out.LastSnapshotAt = b.CreatedAt
			return out, nil
		}
	}
	if len(rows) > 0 {
		out.LastSnapshotAt = rows[len(rows)-1].CreatedAt
	}
	return out, nil
}
func (s *Store) nextBackupCheck() time.Time {
	status, err := s.BackupStatus()
	if err != nil {
		return time.Time{}
	}
	if !status.CheckedAt.IsZero() {
		return status.CheckedAt.Add(24 * time.Hour)
	}
	if !status.LastSnapshotAt.IsZero() {
		return status.LastSnapshotAt.Add(24 * time.Hour)
	}
	return time.Time{}
}
