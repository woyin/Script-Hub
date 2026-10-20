package manager

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type stageStat struct {
	Count   uint64  `json:"count"`
	Seconds float64 `json:"seconds"`
}
type managerStats struct {
	sourceHits, sourceMisses, notModified, fetchErrors, outputHits, outputMisses, staleResponses, exportErrors atomic.Uint64
	mu                                                                                                         sync.Mutex
	stages                                                                                                     map[string]stageStat
}

func (s *managerStats) observe(name string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stages == nil {
		s.stages = map[string]stageStat{}
	}
	v := s.stages[name]
	v.Count++
	v.Seconds += d.Seconds()
	s.stages[name] = v
}
func (s *managerStats) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	stages := map[string]stageStat{}
	for k, v := range s.stages {
		stages[k] = v
	}
	return map[string]any{"sourceHits": s.sourceHits.Load(), "sourceMisses": s.sourceMisses.Load(), "notModified": s.notModified.Load(), "fetchErrors": s.fetchErrors.Load(), "outputHits": s.outputHits.Load(), "outputMisses": s.outputMisses.Load(), "staleResponses": s.staleResponses.Load(), "exportErrors": s.exportErrors.Load(), "stages": stages}
}
func (h *Handler) RenderMetrics(w io.Writer) {
	for name, value := range map[string]uint64{"source_hits": h.stats.sourceHits.Load(), "source_misses": h.stats.sourceMisses.Load(), "source_not_modified": h.stats.notModified.Load(), "fetch_errors": h.stats.fetchErrors.Load(), "output_hits": h.stats.outputHits.Load(), "output_misses": h.stats.outputMisses.Load(), "stale_responses": h.stats.staleResponses.Load(), "export_errors": h.stats.exportErrors.Load()} {
		fmt.Fprintf(w, "# TYPE scripthub_manager_%s_total counter\nscripthub_manager_%s_total %d\n", name, name, value)
	}
	h.stats.mu.Lock()
	defer h.stats.mu.Unlock()
	for name, value := range h.stats.stages {
		fmt.Fprintf(w, "scripthub_manager_stage_seconds_sum{stage=%q} %g\nscripthub_manager_stage_seconds_count{stage=%q} %d\n", name, value.Seconds, name, value.Count)
	}
}

type targetStatus struct {
	LastAttempt time.Time `json:"lastAttempt"`
	LastSuccess time.Time `json:"lastSuccess,omitempty"`
	Error       string    `json:"error,omitempty"`
}
type ResourceStatus struct {
	ID          string                  `json:"id"`
	Kind        string                  `json:"kind"`
	Revision    string                  `json:"revision"`
	LastAttempt time.Time               `json:"lastAttempt,omitempty"`
	LastSuccess time.Time               `json:"lastSuccess,omitempty"`
	NextRun     time.Time               `json:"nextRun,omitempty"`
	Failures    int                     `json:"failures"`
	Error       string                  `json:"error,omitempty"`
	Targets     map[string]targetStatus `json:"targets,omitempty"`
}
type runtimeState struct {
	mu        sync.Mutex
	path      string
	Resources map[string]ResourceStatus `json:"resources"`
}

func newRuntimeState(root string) (*runtimeState, error) {
	dir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &runtimeState{path: filepath.Join(dir, "status.json"), Resources: map[string]ResourceStatus{}}
	if f, err := os.Open(s.path); err == nil {
		defer f.Close()
		if err = json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&s.Resources); err != nil {
			return nil, fmt.Errorf("invalid manager refresh status")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.Resources == nil {
		s.Resources = map[string]ResourceStatus{}
	}
	return s, nil
}
func atomicJSON(path string, v any) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = json.NewEncoder(f).Encode(v); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (s *runtimeState) get(r Record) ResourceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.Resources[r.Kind+"/"+r.ID]
	if v.Revision != revision(r) {
		return ResourceStatus{ID: r.ID, Kind: r.Kind, Revision: revision(r)}
	}
	return v
}
func (s *runtimeState) update(r Record, fn func(*ResourceStatus)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.Kind + "/" + r.ID
	v := s.Resources[key]
	if v.Revision != revision(r) {
		v = ResourceStatus{ID: r.ID, Kind: r.Kind, Revision: revision(r)}
	}
	targets := map[string]targetStatus{}
	for k, status := range v.Targets {
		targets[k] = status
	}
	v.Targets = targets
	fn(&v)
	s.Resources[key] = v
	return atomicJSON(s.path, s.Resources)
}
func (s *runtimeState) prune(rows []Summary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]bool{}
	for _, r := range rows {
		keep[r.Kind+"/"+r.ID] = true
	}
	changed := false
	for k := range s.Resources {
		if !keep[k] {
			delete(s.Resources, k)
			changed = true
		}
	}
	if changed {
		return atomicJSON(s.path, s.Resources)
	}
	return nil
}
