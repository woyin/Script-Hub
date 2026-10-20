package manager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Replacing an original API collection is one transaction, including removals.
// Reused names keep native IDs and share permissions. Other resource kinds are
// copied unchanged; readers see the entire old or entire new generation.
func (s *Store) subStoreReplace(kind string, docs []subStoreDocument) error {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(docs) > MaxResources {
		return ErrLimit
	}
	records := make([]Record, 0, len(s.index)+len(docs))
	names := map[string]bool{}
	ids := map[string]bool{}
	for i, doc := range docs {
		if doc == nil {
			return ErrInvalid
		}
		name, err := subStoreString(doc, "name")
		if err != nil {
			return err
		}
		if names[name] {
			return errSubStoreDuplicate
		}
		names[name] = true
		old, err := s.subStoreLookupLocked(kind, name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		r, err := recordFromDocument(kind, doc, old)
		if err != nil {
			return err
		}
		if r.ID == "" {
			token, err := randomToken()
			if err != nil {
				return err
			}
			r.ID = token[:32]
		}
		if ids[r.ID] {
			return errSubStoreDuplicate
		}
		ids[r.ID] = true
		r.SubStorePosition = int64(i + 1)
		records = append(records, r)
	}
	for id, m := range s.index {
		if m.Summary.Kind != kind {
			r, err := s.get(id)
			if err != nil {
				return err
			}
			records = append(records, r)
		}
	}
	if len(records) > MaxResources {
		return ErrLimit
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	stage := filepath.Join(s.root, "records-"+token)
	if err = os.Mkdir(stage, 0700); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()
	var used int64
	index := map[string]resourceMeta{}
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		used += int64(len(b))
		if len(b) > MaxDocumentBytes || used > s.maxBytes {
			return ErrLimit
		}
		if err = os.WriteFile(filepath.Join(stage, r.ID+".json"), b, 0600); err != nil {
			return err
		}
		index[r.ID] = metadataFor(r)
	}
	if _, err = s.snapshotLocked(); err != nil {
		return errors.New("cannot create safety backup; replacement cancelled")
	}
	if err = s.commitGenerationLocked(stage, index); err != nil {
		return err
	}
	committed = true
	return nil
}
