package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// A document is the original API's flat resource, not the native {spec: ...} DTO.
// Metadata stays beside the native record, so both APIs, backups and shares use
// one resource identity. Content and effective source URLs are not duplicated.
type subStoreDocument map[string]json.RawMessage

var errSubStoreDuplicate = errors.New("duplicate Sub-Store name")
var errSubStoreAmbiguous = errors.New("ambiguous Sub-Store name")
var errSubStoreUnsupported = errors.New("unsupported Sub-Store feature")

func setSubStore(doc subStoreDocument, key string, value any) { doc[key], _ = json.Marshal(value) }
func documentFor(r Record) subStoreDocument {
	doc := subStoreDocument{}
	for k, v := range r.SubStore {
		doc[k] = append(json.RawMessage(nil), v...)
	}
	setSubStore(doc, "name", r.Spec.Name)
	setSubStore(doc, "source", r.Spec.Source)
	if r.Spec.Source == "local" {
		setSubStore(doc, "content", r.Spec.Content)
	} else {
		setSubStore(doc, "url", strings.Join(r.Spec.URLs, "\n"))
		if ua := r.Spec.Headers["User-Agent"]; ua != "" {
			setSubStore(doc, "userAgent", ua)
		}
		if len(r.Spec.Headers) > 0 {
			setSubStore(doc, "headers", r.Spec.Headers)
		}
	}
	if _, ok := doc["process"]; !ok {
		setSubStore(doc, "process", []any{})
	}
	if b, _ := json.Marshal(r.Spec.Processing); string(b) != "{}" {
		setSubStore(doc, "processing", r.Spec.Processing)
	}
	return doc
}
func subStoreString(doc subStoreDocument, key string) (string, error) {
	v, ok := doc[key]
	if !ok || string(v) == "null" {
		return "", nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", fmt.Errorf("%w: %s must be a string", ErrInvalid, key)
	}
	return s, nil
}
func meaningfulSubStore(v json.RawMessage) bool {
	s := strings.TrimSpace(string(v))
	return s != "" && s != "null" && s != "false" && s != "\"\"" && s != "[]" && s != "{}"
}
func recordFromDocument(kind string, doc subStoreDocument, old Record) (Record, error) {
	for _, k := range []string{"mergeSources", "proxy", "ignoreFailedRemoteSub", "ignoreFailedRemoteFile", "responseTransformers", "agePublicKey", "sourceType", "sourceName"} {
		if meaningfulSubStore(doc[k]) {
			return Record{}, fmt.Errorf("%w: %s", errSubStoreUnsupported, k)
		}
	}
	if value, ok := doc["process"]; ok && string(value) != "null" {
		var operations []map[string]json.RawMessage
		if json.Unmarshal(value, &operations) != nil {
			return Record{}, fmt.Errorf("%w: process must be an array", ErrInvalid)
		}
		for _, op := range operations {
			var disabled bool
			_ = json.Unmarshal(op["disabled"], &disabled)
			if !disabled {
				return Record{}, fmt.Errorf("%w: process operations", errSubStoreUnsupported)
			}
		}
	}
	name, err := subStoreString(doc, "name")
	if err != nil {
		return Record{}, err
	}
	if strings.Contains(name, "/") && kind == "subscriptions" {
		return Record{}, fmt.Errorf("%w: subscription name cannot contain slash", ErrInvalid)
	}
	source, err := subStoreString(doc, "source")
	if err != nil {
		return Record{}, err
	}
	rawURL, err := subStoreString(doc, "url")
	if err != nil {
		return Record{}, err
	}
	content, err := subStoreString(doc, "content")
	if err != nil {
		return Record{}, err
	}
	if source == "" {
		source = "local"
		if rawURL != "" {
			source = "remote"
		}
	}
	spec := old.Spec
	spec.Name = name
	spec.Source = source
	spec.Content = ""
	spec.URLs = nil
	spec.Headers = nil
	if source == "local" {
		spec.Content = content
		spec.Refresh.Enabled = false
	} else {
		for _, line := range strings.FieldsFunc(rawURL, func(r rune) bool { return r == '\n' || r == '\r' }) {
			if line = strings.TrimSpace(line); line != "" {
				spec.URLs = append(spec.URLs, line)
			}
		}
		if value, ok := doc["headers"]; ok && string(value) != "null" {
			if json.Unmarshal(value, &spec.Headers) != nil {
				return Record{}, fmt.Errorf("%w: headers must be string values", ErrInvalid)
			}
		}
		if _, explicit := doc["userAgent"]; explicit {
			for key := range spec.Headers {
				if strings.EqualFold(key, "User-Agent") {
					delete(spec.Headers, key)
				}
			}
		}
		ua, err := subStoreString(doc, "userAgent")
		if err != nil {
			return Record{}, err
		}
		if ua != "" {
			if spec.Headers == nil {
				spec.Headers = map[string]string{}
			}
			spec.Headers["User-Agent"] = ua
		}
		normalized := map[string]string{}
		for key, value := range spec.Headers {
			canonical := http.CanonicalHeaderKey(key)
			if _, ok := normalized[canonical]; ok {
				return Record{}, fmt.Errorf("%w: duplicate source header", ErrInvalid)
			}
			normalized[canonical] = value
		}
		spec.Headers = normalized
	}
	if value, ok := doc["processing"]; ok {
		if json.Unmarshal(value, &spec.Processing) != nil {
			return Record{}, fmt.Errorf("%w: invalid processing options", ErrInvalid)
		}
	}
	if kind == "files" {
		typ, err := subStoreString(doc, "type")
		if err != nil {
			return Record{}, err
		}
		if typ != "" && typ != "text" {
			return Record{}, fmt.Errorf("%w: only text files are supported", errSubStoreUnsupported)
		}
	}
	if err := ValidateSpec(kind, spec); err != nil {
		return Record{}, err
	}
	r := old
	r.Kind = kind
	r.Spec = spec
	r.UpdatedAt = time.Now().UTC()
	r.SubStore = map[string]json.RawMessage{}
	for k, v := range doc {
		r.SubStore[k] = append(json.RawMessage(nil), v...)
	}
	for _, k := range []string{"name", "source", "id", "_id", "subscriptions", "processing"} {
		delete(r.SubStore, k)
	}
	if source == "local" {
		delete(r.SubStore, "content")
	} else {
		for _, k := range []string{"url", "userAgent", "headers"} {
			delete(r.SubStore, k)
		}
	}
	return r, nil
}
func (s *Store) subStoreLookupLocked(kind, name string) (Record, error) {
	id := ""
	for key, m := range s.index {
		if m.Summary.Kind == kind && m.Summary.Name == name {
			if id != "" {
				return Record{}, errSubStoreAmbiguous
			}
			id = key
		}
	}
	if id == "" {
		if m, ok := s.index[name]; ok && m.Summary.Kind == kind {
			id = name
		}
	}
	if id == "" {
		return Record{}, ErrNotFound
	}
	return s.get(id)
}
func (s *Store) subStoreGet(kind, name string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subStoreLookupLocked(kind, name)
}
func (s *Store) subStoreRecords(kind string) ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := make([]Record, 0)
	for id, m := range s.index {
		if m.Summary.Kind == kind {
			r, err := s.get(id)
			if err != nil {
				return nil, err
			}
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SubStorePosition != rows[j].SubStorePosition {
			return rows[i].SubStorePosition < rows[j].SubStorePosition
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}
func (s *Store) subStoreSave(kind, name string, patch subStoreDocument, create bool, expected string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := Record{}
	doc := subStoreDocument{}
	if !create {
		var err error
		old, err = s.subStoreLookupLocked(kind, name)
		if err != nil {
			return Record{}, err
		}
		if expected != "" && expected != revision(old) {
			return Record{}, ErrConflict
		}
		doc = documentFor(old)
	}
	for key, value := range patch {
		doc[key] = value
	}
	if _, headersChanged := patch["headers"]; headersChanged {
		if _, agentChanged := patch["userAgent"]; !agentChanged {
			delete(doc, "userAgent")
		}
	}
	if !create {
		if n, err := subStoreString(doc, "name"); err == nil && n == "" {
			setSubStore(doc, "name", old.Spec.Name)
		}
	}
	r, err := recordFromDocument(kind, doc, old)
	if err != nil {
		return Record{}, err
	}
	for id, m := range s.index {
		if id != old.ID && m.Summary.Kind == kind && (m.Summary.Name == r.Spec.Name || id == r.Spec.Name) {
			return Record{}, errSubStoreDuplicate
		}
	}
	if create {
		token, err := randomToken()
		if err != nil {
			return Record{}, err
		}
		r.ID = token[:32]
		var first int64
		for _, m := range s.index {
			if m.Summary.Kind == kind && m.Position < first {
				first = m.Position
			}
		}
		r.SubStorePosition = first - 1
	}
	if err := s.put(r); err != nil {
		return Record{}, err
	}
	return r, nil
}
func (s *Store) subStoreDelete(kind, name, expected string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.subStoreLookupLocked(kind, name)
	if err != nil {
		return err
	}
	if expected != "" && expected != revision(r) {
		return ErrConflict
	}
	return s.deleteLocked(r)
}
