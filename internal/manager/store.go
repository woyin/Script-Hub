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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/script-hub-org/script-hub/internal/subscription"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrInvalid  = errors.New("invalid resource")
	ErrLimit    = errors.New("management data limit exceeded")
)

const MaxDocumentBytes = 8 << 20
const MaxResources = 256

var resourceID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Spec struct {
	Name       string               `json:"name"`
	Source     string               `json:"source"` // local or remote
	Content    string               `json:"content,omitempty"`
	URLs       []string             `json:"urls,omitempty"`
	Headers    map[string]string    `json:"headers,omitempty"`
	Processing subscription.Options `json:"processing,omitempty"`
}
type Record struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Spec        Spec      `json:"spec"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ShareDigest string    `json:"shareDigest,omitempty"`
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

type Store struct {
	mu       sync.RWMutex
	dir      string
	maxBytes int64
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
	s := &Store{dir: dir, maxBytes: maxBytes}
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
	// Only our own pending files are cleaned; resource documents are permanent data.
	probe, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return nil, errors.New("management data directory is not writable")
	}
	probe.Close()
	os.Remove(probe.Name())
	return s, nil
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
	for key, value := range s.Headers {
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
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := s.get(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if r.Kind == kind {
			out = append(out, r.Summary())
		}
	}
	return out, nil
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
	return nil
}
func (s *Store) Save(kind, id string, spec Spec) (Record, error) {
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
		r.ID = id
		r.ShareDigest = old.ShareDigest
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
	return os.Remove(s.path(id))
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
