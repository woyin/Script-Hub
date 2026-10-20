package manager

import (
	"context"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

func refreshTargets(r Record) []string {
	if r.Kind == "files" {
		return []string{"text"}
	}
	if len(r.Spec.Refresh.Targets) > 0 {
		return r.Spec.Refresh.Targets
	}
	return []string{"shadowrocket", "loon", "egern"}
}
func interval(r Record) time.Duration {
	v := r.Spec.Refresh.IntervalSeconds
	if v == 0 {
		v = 21600
	}
	return time.Duration(v) * time.Second
}
func jitter(r Record, d time.Duration) time.Duration {
	v, _ := strconv.ParseUint(r.ID[:4], 16, 16)
	return time.Duration(v%1000) * d / 10000
}
func (h *Handler) current(r Record, fn func()) bool {
	h.store.mu.RLock()
	defer h.store.mu.RUnlock()
	v, ok := h.store.index[r.ID]
	if !ok || v.Summary.Kind != r.Kind || revision(v.record()) != revision(r) {
		return false
	}
	fn()
	return true
}
func (h *Handler) note(r Record, target string, started time.Time, err error) {
	if errors.Is(err, errSourceCoolingDown) {
		return
	}
	h.current(r, func() {
		if e := h.runtime.update(r, func(s *ResourceStatus) {
			v := s.Targets[target]
			if started.After(v.LastAttempt) {
				v.LastAttempt = started
			}
			if started.After(s.LastAttempt) {
				s.LastAttempt = started
			}
			if err != nil {
				v.Error = "refresh or conversion failed"
			} else {
				v.Error = ""
				v.LastSuccess = time.Now().UTC()
			}
			s.Targets[target] = v
			s.Error = ""
			for _, status := range s.Targets {
				if status.Error != "" {
					s.Error = "one or more targets failed"
					break
				}
			}
			if s.Error == "" {
				s.LastSuccess = time.Now().UTC()
			}
		}); e != nil {
			log.Print("manager refresh status persistence failed")
		}
	})
}
func (h *Handler) allRows() ([]Summary, error) { return h.store.List("") }
func (h *Handler) statuses() ([]ResourceStatus, error) {
	rows, err := h.allRows()
	if err != nil {
		return nil, err
	}
	out := make([]ResourceStatus, 0, len(rows))
	for _, r := range rows {
		out = append(out, h.runtime.get(Record{ID: r.ID, Kind: r.Kind, UpdatedAt: r.UpdatedAt}))
	}
	return out, nil
}

type scheduledJob struct {
	revision string
	cancel   context.CancelFunc
}
type dueResource struct {
	meta resourceMeta
	due  time.Time
}

// Wake on mutations, completed work or the nearest deadline. No periodic document scan.
func (h *Handler) Start() {
	h.bgMu.Lock()
	defer h.bgMu.Unlock()
	if h.bgDone != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.bgCancel = cancel
	done := make(chan struct{})
	h.bgDone = done
	go func() {
		defer func() { h.jobs.Wait(); close(done) }()
		for {
			if ctx.Err() != nil {
				return
			}
			next := h.step(ctx, time.Now().UTC())
			delay := time.Until(next)
			if delay <= 0 {
				delay = time.Millisecond
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-h.store.changed:
			case <-h.runtime.changed:
			case <-h.wake:
			case <-timer.C:
			}
			timer.Stop()
		}
	}()
}
func (h *Handler) Stop(ctx context.Context) error {
	h.bgMu.Lock()
	done, cancel := h.bgDone, h.bgCancel
	h.bgMu.Unlock()
	if done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// step dispatches every due job that can run without consuming the foreground reserve.
// Tests can supply a logical clock and wait for jobs to finish without long sleeps.
func (h *Handler) step(ctx context.Context, now time.Time) time.Time {
	if ctx.Err() != nil {
		return now.Add(time.Hour)
	}
	rows, err := h.allRows()
	if err != nil {
		return now.Add(time.Minute)
	}
	if err = h.runtime.prune(rows); err != nil {
		log.Print("manager refresh status cleanup failed")
	}
	if !now.Before(h.nextBackup) {
		h.nextBackup = now.Add(24 * time.Hour)
		if _, err = h.store.SnapshotIfChanged(now); err != nil {
			h.nextBackup = now.Add(time.Hour)
			log.Print("manager automatic backup check failed")
		}
	}
	next := h.nextBackup
	h.bgMu.Lock()
	for key, job := range h.scheduled {
		kind, id, _ := strings.Cut(key, "/")
		m, err := h.store.Meta(kind, id)
		if err != nil || revision(m.record()) != job.revision || !m.Refresh.Enabled {
			job.cancel()
		}
	}
	h.bgMu.Unlock()
	due := []dueResource{}
	for _, row := range rows {
		m, err := h.store.Meta(row.Kind, row.ID)
		if err != nil || !m.Refresh.Enabled {
			continue
		}
		key := row.Kind + "/" + row.ID
		h.bgMu.Lock()
		running := h.scheduled[key] != nil
		h.bgMu.Unlock()
		if running {
			continue
		}
		status := h.runtime.get(m.record())
		at := status.NextRun
		if at.IsZero() {
			at = now
		}
		if at.After(now) {
			if at.Before(next) {
				next = at
			}
			continue
		}
		due = append(due, dueResource{m, at})
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].due.Equal(due[j].due) {
			return due[i].meta.Summary.ID < due[j].meta.Summary.ID
		}
		return due[i].due.Before(due[j].due)
	})
	reserve := 0
	if cap(h.slots) > 1 {
		reserve = 1
	}
	for _, candidate := range due {
		if ctx.Err() != nil || h.foregroundWaiters.Load() > 0 || len(h.slots) >= cap(h.slots)-reserve {
			break
		}
		key := candidate.meta.Summary.Kind + "/" + candidate.meta.Summary.ID
		h.bgMu.Lock()
		if len(h.scheduled) >= max(1, cap(h.slots)-1) {
			h.bgMu.Unlock()
			break
		}
		if h.scheduled[key] != nil {
			h.bgMu.Unlock()
			continue
		}
		select {
		case h.slots <- struct{}{}:
		default:
			h.bgMu.Unlock()
			return next
		}
		jobCtx, cancel := context.WithTimeout(ctx, h.cfg.Timeout)
		job := &scheduledJob{revision: revision(candidate.meta.record()), cancel: cancel}
		h.scheduled[key] = job
		h.jobs.Add(1)
		h.bgMu.Unlock()
		go h.runScheduled(jobCtx, job, key, candidate.meta, now)
	}
	return next
}
func (h *Handler) runScheduled(ctx context.Context, job *scheduledJob, key string, meta resourceMeta, now time.Time) {
	defer func() {
		job.cancel()
		<-h.slots
		h.bgMu.Lock()
		delete(h.scheduled, key)
		h.bgMu.Unlock()
		h.signal()
		h.jobs.Done()
	}()
	r, err := h.store.Get(meta.Summary.Kind, meta.Summary.ID)
	if err != nil || revision(r) != job.revision || !r.Spec.Refresh.Enabled {
		return
	}
	err = h.refreshResource(ctx, r)
	if errors.Is(err, context.Canceled) {
		return
	}
	completed := time.Now().UTC()
	if completed.Before(now) {
		completed = now
	}
	h.current(r, func() {
		if e := h.runtime.update(r, func(s *ResourceStatus) {
			delay := interval(r)
			if err != nil {
				s.Failures = min(6, s.Failures+1)
				delay = time.Minute * time.Duration(1<<uint(s.Failures-1))
				s.Error = "scheduled refresh failed"
			} else {
				s.Failures = 0
				s.Error = ""
				s.LastSuccess = completed
			}
			s.NextRun = completed.Add(delay + jitter(r, delay))
		}); e != nil {
			log.Print("manager schedule persistence failed")
		}
	})
}
func (h *Handler) refreshResource(ctx context.Context, r Record) error {
	key := r.Kind + "/" + r.ID + "/" + revision(r)
	h.mu.Lock()
	if f := h.refreshFlights[key]; f != nil {
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.done:
			return f.err
		}
	}
	f := &flight{done: make(chan struct{})}
	h.refreshFlights[key] = f
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.refreshFlights, key); close(f.done); h.mu.Unlock() }()
	started := time.Now().UTC()
	ctx = context.WithValue(ctx, refreshSourceKey{}, started)
	if r.Kind == "subscriptions" {
		nodes, err := h.readNodes(ctx, r)
		if err != nil {
			f.err = err
			for _, target := range refreshTargets(r) {
				h.note(r, target, started, err)
			}
			return err
		}
		ctx = context.WithValue(ctx, preparedNodesKey{}, nodes)
	}
	for _, target := range refreshTargets(r) {
		if err := ctx.Err(); err != nil {
			f.err = err
			return err
		}
		rd, _, err := h.export(ctx, r, target, true)
		closeBody(rd)
		if err != nil {
			f.err = errors.Join(f.err, err)
		}
	}
	return f.err
}
