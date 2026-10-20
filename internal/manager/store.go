// Package manager provides private subscription/file management and scoped sharing.
package manager

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/script-hub-org/script-hub/internal/subscription"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrInvalid  = errors.New("invalid resource")
	ErrConflict = errors.New("resource changed; reload before saving")
	ErrLimit    = errors.New("management data limit exceeded")
)

const MaxDocumentBytes = 8 << 20
const MaxResources = 256

var resourceID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type RefreshPolicy struct {
	Enabled         bool     `json:"enabled,omitempty"`
	IntervalSeconds int      `json:"intervalSeconds,omitempty"`
	Targets         []string `json:"targets,omitempty"`
	DisableStale    bool     `json:"disableStale,omitempty"`
}

func revision(r Record) string {
	return digest(r.ID + "@" + r.UpdatedAt.UTC().Format(time.RFC3339Nano))
}

type Spec struct {
	Refresh    RefreshPolicy        `json:"refresh,omitempty"`
	Name       string               `json:"name"`
	Source     string               `json:"source"` // local or remote
	Content    string               `json:"content,omitempty"`
	URLs       []string             `json:"urls,omitempty"`
	Headers    map[string]string    `json:"headers,omitempty"`
	Processing subscription.Options `json:"processing,omitempty"`
}
type Record struct {
	ID               string                     `json:"id"`
	Kind             string                     `json:"kind"`
	Spec             Spec                       `json:"spec"`
	UpdatedAt        time.Time                  `json:"updatedAt"`
	ShareDigest      string                     `json:"shareDigest,omitempty"`
	SubStore         map[string]json.RawMessage `json:"substore,omitempty"`
	SubStorePosition int64                      `json:"substorePosition,omitempty"`
}
type Summary struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updatedAt"`
	Shared    bool      `json:"shared"`
}

func (r Record) Summary() Summary {
	return Summary{r.ID, r.Kind, r.Spec.Name, r.Spec.Source, r.UpdatedAt, r.ShareDigest != ""}
}

// resourceMeta deliberately excludes URLs, headers and content. The index is rebuilt
// once at startup; mutations publish disk data before updating this small index.
type resourceMeta struct {
	Summary     Summary
	Refresh     RefreshPolicy
	ShareDigest string
	Digest      string
	Position    int64
}

func metadataFor(r Record) resourceMeta {
	semantic := r
	semantic.UpdatedAt = time.Time{}
	b, _ := json.Marshal(semantic)
	policy := r.Spec.Refresh
	policy.Targets = append([]string(nil), policy.Targets...)
	return resourceMeta{r.Summary(), policy, r.ShareDigest, digest(string(b)), r.SubStorePosition}
}
func (m resourceMeta) record() Record {
	return Record{ID: m.Summary.ID, Kind: m.Summary.Kind, UpdatedAt: m.Summary.UpdatedAt, ShareDigest: m.ShareDigest, Spec: Spec{Refresh: m.Refresh}}
}

type Store struct {
	mu         sync.RWMutex
	backupMu   sync.Mutex
	backupPins map[string]int
	dir        string
	root       string
	maxBytes   int64
	index      map[string]resourceMeta
	changed    chan struct{}
}

func NewStore(dir string, maxBytes int64) (*Store, error) {
	if maxBytes <= 0 {
		maxBytes = 32 << 20
	}
	if dir == "" {
		return nil, errors.New("management data directory is empty")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create management data directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, errors.New("cannot secure management data directory")
	}
	s := &Store{dir: dir, root: dir, maxBytes: maxBytes, backupPins: map[string]int{}, index: map[string]resourceMeta{}, changed: make(chan struct{}, 1)}
	if b, err := os.ReadFile(filepath.Join(dir, ".active")); err == nil {
		name := string(b)
		if !regexp.MustCompile(`^records-[a-f0-9]{64}$`).MatchString(name) {
			return nil, errors.New("invalid management active generation")
		}
		s.dir = filepath.Join(dir, name)
		if info, err := os.Stat(s.dir); err != nil || !info.IsDir() {
			return nil, errors.New("missing management active generation")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pending-") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return nil, err
			}
		}
	}
	// Retire uncommitted/old restore generations after a crash. Never follow symlinks.
	for _, entry := range entries {
		if regexp.MustCompile(`^records-[a-f0-9]{64}$`).MatchString(entry.Name()) && filepath.Join(s.root, entry.Name()) != s.dir {
			if err := os.RemoveAll(filepath.Join(s.root, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	if s.dir != s.root {
		active, err := os.ReadDir(s.dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range active {
			if strings.HasPrefix(entry.Name(), ".pending-") {
				if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, sub := range []string{"backups", "runtime"} {
		entries, err := os.ReadDir(filepath.Join(s.root, sub))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".pending-") {
				if err := os.Remove(filepath.Join(s.root, sub, entry.Name())); err != nil {
					return nil, err
				}
			}
		}
	}
	// Only our own pending files are cleaned; resource documents are permanent data.
	probe, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return nil, errors.New("management data directory is not writable")
	}
	probe.Close()
	os.Remove(probe.Name())
	active, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range active {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		r, err := s.get(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		s.index[r.ID] = metadataFor(r)
	}
	return s, nil
}
func (s *Store) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}
func (s *Store) Meta(kind, id string) (resourceMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.index[id]
	if !ok || m.Summary.Kind != kind {
		return resourceMeta{}, ErrNotFound
	}
	m.Refresh.Targets = append([]string(nil), m.Refresh.Targets...)
	return m, nil
}
func ValidateSpec(kind string, s Spec) error {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", ErrInvalid, message) }
	if kind != "subscriptions" && kind != "files" {
		return invalid("unknown resource kind")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 256 || strings.ContainsAny(s.Name, "\r\n\x00") {
		return invalid("name must contain 1–256 bytes without control lines")
	}
	if s.Source != "local" && s.Source != "remote" {
		return invalid("source must be local or remote")
	}
	if len(s.Content) > MaxDocumentBytes {
		return ErrLimit
	}
	if s.Source == "local" {
		if len(s.URLs) != 0 || len(s.Headers) != 0 {
			return invalid("local source cannot have URLs or headers")
		}
	} else {
		if s.Content != "" || len(s.URLs) == 0 || len(s.URLs) > 16 {
			return invalid("remote source requires 1–16 URLs and no content")
		}
		if kind == "files" && len(s.URLs) != 1 {
			return invalid("remote files require exactly one URL")
		}
		for _, raw := range s.URLs {
			u, err := url.Parse(raw)
			if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" || len(raw) > 8192 {
				return invalid("source URLs must be HTTP(S), without userinfo or fragment")
			}
		}
	}
	if len(s.Headers) > 32 {
		return invalid("too many headers")
	}
	headerNames := map[string]bool{}
	for key, value := range s.Headers {
		canonical := http.CanonicalHeaderKey(key)
		if headerNames[canonical] {
			return invalid("duplicate source header")
		}
		headerNames[canonical] = true
		for _, c := range key {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
				return invalid("invalid source header name")
			}
		}
		if key == "" || len(key)+len(value) > 8192 || strings.ContainsAny(key+value, "\r\n\x00") || strings.ContainsAny(key, " :\t") {
			return invalid("invalid source header")
		}
	}
	if kind == "files" {
		b, _ := json.Marshal(s.Processing)
		if string(b) != "{}" {
			return invalid("files do not accept node processing")
		}
	}
	if s.Refresh.IntervalSeconds != 0 && (s.Refresh.IntervalSeconds < 60 || s.Refresh.IntervalSeconds > 30*86400) {
		return invalid("refresh interval must be 60–2592000 seconds")
	}
	if s.Refresh.Enabled && s.Source != "remote" {
		return invalid("automatic refresh requires a remote source")
	}
	seen := map[string]bool{}
	for _, target := range s.Refresh.Targets {
		if seen[target] {
			return invalid("duplicate refresh target")
		}
		seen[target] = true
		switch target {
		case "shadowrocket", "loon", "egern", "clash", "uri":
			if kind == "files" {
				return invalid("file refresh has no target list")
			}
		default:
			return invalid("invalid refresh target")
		}
	}
	if err := s.Processing.Validate(); err != nil {
		return invalid(err.Error())
	}
	return nil
}
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func digest(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func CheckShare(r Record, token string) bool {
	return r.ShareDigest != "" && len(token) == 64 && subtle.ConstantTimeCompare([]byte(digest(token)), []byte(r.ShareDigest)) == 1
}
func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }
func (s *Store) get(id string) (Record, error) {
	if !resourceID.MatchString(id) {
		return Record{}, ErrNotFound
	}
	f, err := os.Open(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, errors.New("cannot read resource")
	}
	defer f.Close()
	var r Record
	d := json.NewDecoder(io.LimitReader(f, MaxDocumentBytes+1))
	if err := d.Decode(&r); err != nil {
		return Record{}, errors.New("invalid stored resource")
	}
	if r.ID != id || (r.Kind != "subscriptions" && r.Kind != "files") {
		return Record{}, errors.New("invalid stored resource")
	}
	return r, nil
}
func (s *Store) Get(kind, id string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, err := s.get(id)
	if err == nil && r.Kind != kind {
		return Record{}, ErrNotFound
	}
	return r, err
}
func (s *Store) List(kind string) ([]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := make([]Summary, 0, len(s.index))
	for _, meta := range s.index {
		if kind == "" || meta.Summary.Kind == kind {
			rows = append(rows, meta.Summary)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}
func (s *Store) put(r Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > MaxDocumentBytes {
		return ErrLimit
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var used int64
	count := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		used += info.Size()
		count++
	}
	_, oldErr := os.Stat(s.path(r.ID))
	if errors.Is(oldErr, os.ErrNotExist) && count >= MaxResources {
		return ErrLimit
	}
	// Atomic replacement temporarily needs old+new bytes; charge only this document's real size.
	if used+int64(len(data)) > s.maxBytes {
		return ErrLimit
	}
	f, err := os.CreateTemp(s.dir, ".pending-")
	if err != nil {
		return errors.New("cannot write resource")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return errors.New("cannot write resource")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path(r.ID)); err != nil {
		return errors.New("cannot publish resource")
	}
	s.index[r.ID] = metadataFor(r)
	s.notify()
	return nil
}
func (s *Store) Save(kind, id string, spec Spec) (Record, error) {
	return s.SaveVersion(kind, id, spec, "")
}
func (s *Store) SaveVersion(kind, id string, spec Spec, expected string) (Record, error) {
	if err := ValidateSpec(kind, spec); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := Record{Kind: kind, Spec: spec, UpdatedAt: time.Now().UTC()}
	if id == "" {
		token, err := randomToken()
		if err != nil {
			return Record{}, err
		}
		r.ID = token[:32]
	} else {
		old, err := s.get(id)
		if err != nil {
			return Record{}, err
		}
		if old.Kind != kind {
			return Record{}, ErrNotFound
		}
		if expected != "" && expected != revision(old) {
			return Record{}, ErrConflict
		}
		r.ID = id
		r.ShareDigest = old.ShareDigest
		r.SubStore = old.SubStore
		r.SubStorePosition = old.SubStorePosition
	}
	if err := s.put(r); err != nil {
		return Record{}, err
	}
	return r, nil
}
func (s *Store) Delete(kind, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return err
	}
	if r.Kind != kind {
		return ErrNotFound
	}
	return s.deleteLocked(r)
}
func (s *Store) deleteLocked(r Record) error {
	if err := os.Remove(s.path(r.ID)); err != nil {
		return err
	}
	delete(s.index, r.ID)
	s.notify()
	return nil
}

func (s *Store) Share(kind, id string, revoke bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return "", err
	}
	if r.Kind != kind {
		return "", ErrNotFound
	}
	token := ""
	if !revoke {
		token, err = randomToken()
		if err != nil {
			return "", err
		}
		r.ShareDigest = digest(token)
	} else {
		r.ShareDigest = ""
	}
	if err := s.put(r); err != nil {
		return "", err
	}
	return token, nil
}
