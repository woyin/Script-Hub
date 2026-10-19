// Input: context, encoding/json, crypto/sha256, math/rand/v2, os, time
// Output: refreshScheduler
// Pos: API层-单任务后台调度、缓存预检查、退避及脱敏状态持久化
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
)

type backgroundRefreshKey struct{}
type refreshState struct {
	ID                  string    `json:"id"`
	Enabled             bool      `json:"enabled"`
	RequestHash         string    `json:"requestHash"`
	LastAttempt         time.Time `json:"lastAttempt,omitzero"`
	LastSuccess         time.Time `json:"lastSuccess,omitzero"`
	NextRun             time.Time `json:"nextRun,omitzero"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	LastError           string    `json:"lastError,omitempty"`
}
type refreshStatusDocument struct {
	Version   int            `json:"version"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Jobs      []refreshState `json:"jobs"`
}
type refreshScheduler struct {
	jobs       []config.RefreshJob
	states     []refreshState
	statusFile string
	now        func() time.Time
	jitter     func(time.Duration) time.Duration
	execute    func(context.Context, config.RefreshJob) error
	createdAt  func(config.RefreshRequest) (time.Time, bool)
	timeout    time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	once       sync.Once
	done       chan struct{}
}

func newRefreshScheduler(s *Server) *refreshScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	timeout := time.Duration(s.cfg.RequestTimeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	r := &refreshScheduler{jobs: s.cfg.RefreshJobs, statusFile: s.cfg.RefreshStatusFile, now: time.Now, timeout: timeout, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	r.jitter = func(interval time.Duration) time.Duration {
		limit := shorterDuration(interval/10, 30*time.Second)
		if limit <= 0 {
			return 0
		}
		return time.Duration(rand.Int64N(int64(limit)))
	}
	r.createdAt = func(req config.RefreshRequest) (time.Time, bool) {
		return s.cache.CreatedAt(s.refreshKey(req, req.Args))
	}
	r.execute = func(ctx context.Context, job config.RefreshJob) error {
		if s.cache == nil {
			return errors.New("cache unavailable")
		}
		args := make(map[string]string, len(job.Request.Args)+1)
		for k, v := range job.Request.Args {
			args[k] = v
		}
		args["refresh"] = "true"
		ctx = context.WithValue(ctx, backgroundRefreshKey{}, true)
		out, _ := s.runConvert(ctx, job.Request, args)
		if out == nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			return errors.New("conversion failed")
		}
		rd := out.GetResponse()
		if rd.File != nil {
			rd.File.Close()
		}
		if rd.Incomplete || rd.Status != 200 || rd.Headers["X-ScriptHub-Cache"] != "REFRESH" {
			return errors.New("conversion was not cached")
		}
		return nil
	}
	return r
}
func requestHash(req config.RefreshRequest) string {
	data, _ := json.Marshal(req)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (r *refreshScheduler) initialize() {
	previous := make(map[string]refreshState)
	if r.statusFile != "" {
		if f, err := os.Open(r.statusFile); err == nil {
			data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
			f.Close()
			var doc refreshStatusDocument
			if err == nil && len(data) <= 1<<20 && json.Unmarshal(data, &doc) == nil && doc.Version == 1 {
				for _, state := range doc.Jobs {
					previous[state.ID] = state
				}
			} else {
				log.Print("后台刷新: 状态文件无效，重新建立状态")
			}
		} else if !os.IsNotExist(err) {
			log.Print("后台刷新: 状态文件读取失败，重新建立状态")
		}
	}
	now := r.now()
	r.states = make([]refreshState, len(r.jobs))
	for i, job := range r.jobs {
		hash := requestHash(job.Request)
		state := previous[job.ID]
		if state.RequestHash != hash {
			state = refreshState{}
		}
		state.ID = job.ID
		state.Enabled = job.Enabled
		state.RequestHash = hash
		savedNext := state.NextRun
		state.NextRun = time.Time{}
		if job.Enabled {
			interval := time.Duration(job.IntervalSeconds) * time.Second
			due := now
			if created, ok := r.createdAt(job.Request); ok && created.Add(interval).After(now) {
				due = created.Add(interval)
			}
			state.NextRun = due.Add(r.jitter(interval))
			// 恢复持久化的完成后重试时间，不按尝试开始时间重新缩短退避。
			if state.ConsecutiveFailures > 0 {
				retryAt := savedNext
				if retryAt.IsZero() {
					retryAt = state.LastAttempt.Add(r.retryDelay(i, state.ConsecutiveFailures))
				}
				if retryAt.After(state.NextRun) {
					state.NextRun = retryAt
				}
			}
		}
		r.states[i] = state
	}
	r.persist()
}
func (r *refreshScheduler) retryDelay(index, failures int) time.Duration {
	capDelay := shorterDuration(time.Duration(r.jobs[index].IntervalSeconds)*time.Second/2, 30*time.Minute)
	delay := time.Minute
	for n := 1; n < failures && delay < capDelay; n++ {
		delay = shorterDuration(delay*2, capDelay)
	}
	return shorterDuration(delay, capDelay)
}
func (r *refreshScheduler) step(ctx context.Context) bool {
	now := r.now()
	selected := -1
	for i, state := range r.states {
		if state.Enabled && !state.NextRun.After(now) && (selected < 0 || state.NextRun.Before(r.states[selected].NextRun)) {
			selected = i
		}
	}
	if selected < 0 || ctx.Err() != nil {
		return false
	}
	state := &r.states[selected]
	job := r.jobs[selected]
	interval := time.Duration(job.IntervalSeconds) * time.Second
	if created, ok := r.createdAt(job.Request); ok && created.Add(interval).After(now) {
		state.NextRun = created.Add(interval).Add(r.jitter(interval))
		r.persist()
		return true
	}
	state.LastAttempt = now
	r.persist()
	taskCtx, cancel := context.WithTimeout(ctx, r.timeout)
	err := r.execute(taskCtx, job)
	cancel()
	finished := r.now()
	if err == nil {
		state.LastSuccess = finished
		state.ConsecutiveFailures = 0
		state.LastError = ""
		interval := time.Duration(job.IntervalSeconds) * time.Second
		state.NextRun = finished.Add(interval + r.jitter(interval))
		log.Printf("后台刷新任务 %s: success", job.ID)
	} else {
		state.ConsecutiveFailures++
		category := "conversion_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			category = "timeout"
		}
		if errors.Is(err, context.Canceled) {
			category = "canceled"
		}
		state.LastError = category
		delay := r.retryDelay(selected, state.ConsecutiveFailures)
		state.NextRun = finished.Add(delay + r.jitter(delay))
		log.Printf("后台刷新任务 %s: %s, consecutiveFailures=%d", job.ID, category, state.ConsecutiveFailures)
	}
	r.persist()
	return true
}
func (r *refreshScheduler) persist() {
	if r.statusFile == "" {
		return
	}
	err := func() error {
		dir := filepath.Dir(r.statusFile)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		file, err := os.CreateTemp(dir, ".refresh-status-")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		defer file.Close()
		if err := json.NewEncoder(file).Encode(refreshStatusDocument{Version: 1, UpdatedAt: r.now(), Jobs: r.states}); err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		return os.Rename(file.Name(), r.statusFile)
	}()
	if err != nil {
		log.Print("后台刷新: 状态文件写入失败")
	}
}
func (r *refreshScheduler) start() {
	r.once.Do(func() {
		go func() {
			defer close(r.done)
			r.initialize()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if r.step(r.ctx) {
					continue
				}
				select {
				case <-r.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}
func (r *refreshScheduler) stop(ctx context.Context) error {
	r.cancel()
	r.once.Do(func() { close(r.done) })
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func shorterDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
