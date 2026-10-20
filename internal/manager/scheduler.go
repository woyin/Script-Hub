package manager

import (
	"context"
	"errors"
	"log"
	"strconv"
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
	v, err := h.store.get(r.ID)
	if err != nil || v.Kind != r.Kind || revision(v) != revision(r) {
		return false
	}
	fn()
	return true
}
func (h *Handler) note(r Record, target string, started time.Time, err error) {
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
func (h *Handler) allRows() ([]Summary, error) {
	a, err := h.store.List("subscriptions")
	if err != nil {
		return nil, err
	}
	b, err := h.store.List("files")
	return append(a, b...), err
}
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

// The scheduler uses one background worker and the same bounded export slots as requests.
func (h *Handler) Start() {
	h.bgMu.Lock()
	defer h.bgMu.Unlock()
	if h.bgDone != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.bgCancel = cancel
	h.bgDone = make(chan struct{})
	go func() {
		defer close(h.bgDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			h.step(ctx, time.Now().UTC())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (h *Handler) Stop(ctx context.Context) error {
	h.bgMu.Lock()
	done := h.bgDone
	cancel := h.bgCancel
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
func (h *Handler) step(ctx context.Context, now time.Time) {
	rows, err := h.allRows()
	if err != nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err = h.runtime.prune(rows); err != nil {
		log.Print("manager refresh status cleanup failed")
	}
	if len(rows) > 0 && !now.Before(h.nextBackup) {
		h.nextBackup = now.Add(time.Hour)
		backups, e := h.store.Backups()
		if e == nil && (len(backups) == 0 || now.Sub(backups[len(backups)-1].CreatedAt) >= 24*time.Hour) {
			if _, e = h.store.Snapshot(); e != nil {
				log.Print("manager automatic backup failed")
			} else {
				h.nextBackup = now.Add(24 * time.Hour)
			}
		}
	}
	for _, summary := range rows {
		if ctx.Err() != nil {
			return
		}
		status := h.runtime.get(Record{ID: summary.ID, Kind: summary.Kind, UpdatedAt: summary.UpdatedAt})
		if !status.NextRun.IsZero() && now.Before(status.NextRun) {
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, h.cfg.Timeout)
		select {
		case h.slots <- struct{}{}:
		case <-taskCtx.Done():
			cancel()
			return
		}
		r, err := h.store.Get(summary.Kind, summary.ID)
		if err == nil && r.Spec.Refresh.Enabled {
			err = h.refreshResource(taskCtx, r)
			h.current(r, func() {
				_ = h.runtime.update(r, func(s *ResourceStatus) {
					delay := interval(r)
					if err != nil {
						s.Failures++
						if s.Failures > 6 {
							s.Failures = 6
						}
						delay = time.Minute * time.Duration(1<<uint(s.Failures-1))
						s.Error = "scheduled refresh failed"
					} else {
						s.Failures = 0
						s.Error = ""
						s.LastSuccess = time.Now().UTC()
					}
					s.NextRun = now.Add(delay + jitter(r, delay))
				})
			})
			<-h.slots
			cancel()
			return
		}
		<-h.slots
		cancel()
	}
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
