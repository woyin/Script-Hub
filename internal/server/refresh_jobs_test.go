package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
)

func schedulerFixture(t *testing.T) (*Server, *refreshScheduler, *time.Time) {
	t.Helper()
	cfg := config.LoadConfig()
	cfg.CacheDir = t.TempDir()
	cfg.RefreshStatusFile = filepath.Join(t.TempDir(), "status.json")
	cfg.RefreshJobs = []config.RefreshJob{{ID: "example", Enabled: true, IntervalSeconds: 3600, Request: config.RefreshRequest{Type: "rule-set", Target: "surge-rule-set", URLs: []string{"https://example.com/rules?token=private-token"}}}}
	s := New(cfg)
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	r := s.refresher
	now := time.Now()
	r.now = func() time.Time { return now }
	r.jitter = func(time.Duration) time.Duration { return 0 }
	r.createdAt = func(config.RefreshRequest) (time.Time, bool) { return time.Time{}, false }
	return s, r, &now
}
func TestRefreshSchedulerSuccessFailureAndPersistence(t *testing.T) {
	s, r, now := schedulerFixture(t)
	var calls int
	r.execute = func(context.Context, config.RefreshJob) error { calls++; return nil }
	r.initialize()
	if !r.step(context.Background()) || calls != 1 {
		t.Fatal("cold job not run")
	}
	state := r.states[0]
	if state.LastSuccess.IsZero() || !state.NextRun.Equal(now.Add(time.Hour)) {
		t.Fatal("success state")
	}
	if r.step(context.Background()) {
		t.Fatal("job repeated early")
	}
	*now = now.Add(time.Hour)
	r.execute = func(context.Context, config.RefreshJob) error {
		return errors.New("https://secret.example?token=private-token")
	}
	r.step(context.Background())
	if r.states[0].ConsecutiveFailures != 1 || !r.states[0].NextRun.Equal(now.Add(time.Minute)) {
		t.Fatal("first retry")
	}
	*now = now.Add(time.Minute)
	r.step(context.Background())
	if r.states[0].ConsecutiveFailures != 2 || !r.states[0].NextRun.Equal(now.Add(2*time.Minute)) {
		t.Fatal("exponential retry")
	}
	data, err := os.ReadFile(s.cfg.RefreshStatusFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-token") || strings.Contains(string(data), "secret.example") || strings.Contains(string(data), "urls") {
		t.Fatal("status leaked request")
	}
	restored := newRefreshScheduler(s)
	defer restored.stop(context.Background())
	restored.now = r.now
	restored.jitter = r.jitter
	restored.createdAt = r.createdAt
	restored.initialize()
	if restored.states[0].ConsecutiveFailures != 2 || !restored.states[0].LastSuccess.Equal(state.LastSuccess) || !restored.states[0].NextRun.After(*now) {
		t.Fatal("restart lost state/backoff")
	}
}
func TestRefreshSchedulerChecksFreshCacheAndDisabled(t *testing.T) {
	_, r, now := schedulerFixture(t)
	created := *now
	r.createdAt = func(config.RefreshRequest) (time.Time, bool) { return created, true }
	r.execute = func(context.Context, config.RefreshJob) error { t.Fatal("fresh job executed"); return nil }
	r.initialize()
	if !r.states[0].NextRun.Equal(now.Add(time.Hour)) || r.step(context.Background()) {
		t.Fatal("fresh startup")
	}
	// 前台已刷新时，到期任务应重新检查并跳过重复下载。
	*now = now.Add(time.Hour)
	created = *now
	if !r.step(context.Background()) || !r.states[0].NextRun.Equal(now.Add(time.Hour)) {
		t.Fatal("did not defer fresh cache")
	}
	r.jobs[0].Enabled = false
	r.initialize()
	if r.step(context.Background()) || !r.states[0].NextRun.IsZero() {
		t.Fatal("disabled job ran")
	}
}
func TestRefreshSchedulerActualCacheAndPublicLimitBypass(t *testing.T) {
	s, r, now := schedulerFixture(t)
	var hits atomic.Int32
	var fail atomic.Bool
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "payload:\n - DOMAIN,scheduled.example")
	}))
	defer source.Close()
	r.jobs[0].Request.URLs = []string{source.URL}
	// 先耗尽公开刷新配额，后台仍能执行。
	key := s.refreshKey(r.jobs[0].Request, nil)
	s.refreshLimit.allow(key)
	r.initialize()
	r.step(context.Background())
	if hits.Load() != 1 || r.states[0].ConsecutiveFailures != 0 {
		t.Fatal("background did not populate cache")
	}
	cached, ok := s.cache.Get(key)
	if !ok {
		t.Fatal("background cache missing")
	}
	data, _ := io.ReadAll(cached.File.Reader())
	cached.File.Close()
	if !strings.Contains(string(data), "scheduled.example") {
		t.Fatal("wrong cache")
	}
	fail.Store(true)
	*now = now.Add(time.Hour)
	r.step(context.Background())
	if hits.Load() != 2 || r.states[0].ConsecutiveFailures != 1 {
		t.Fatal("background failure not recorded")
	}
	cached, ok = s.cache.Get(key)
	if !ok {
		t.Fatal("failed refresh removed good cache")
	}
	cached.File.Close()
	dirs, _ := filepath.Glob(filepath.Join(s.cfg.CacheDir, ".work-*"))
	if len(dirs) != 0 {
		t.Fatal("background leaked workspace")
	}
}
func TestRefreshSchedulerStopsActiveTask(t *testing.T) {
	_, r, _ := schedulerFixture(t)
	started := make(chan struct{})
	r.execute = func(ctx context.Context, _ config.RefreshJob) error { close(started); <-ctx.Done(); return ctx.Err() }
	r.start()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.stop(ctx); err != nil {
		t.Fatal(err)
	}
	var status refreshStatusDocument
	data, _ := os.ReadFile(r.statusFile)
	if json.Unmarshal(data, &status) != nil || status.Jobs[0].LastError != "canceled" {
		t.Fatal("stop state")
	}
}

func TestRefreshSchedulerRestartKeepsRetryAfterSlowFailure(t *testing.T) {
	s, r, now := schedulerFixture(t)
	r.execute = func(context.Context, config.RefreshJob) error {
		*now = now.Add(30 * time.Second)
		return errors.New("failed")
	}
	r.initialize()
	r.step(context.Background())
	expected := r.states[0].NextRun
	restarted := newRefreshScheduler(s)
	defer restarted.stop(context.Background())
	restarted.now = r.now
	restarted.jitter = r.jitter
	restarted.createdAt = r.createdAt
	restarted.initialize()
	if !restarted.states[0].NextRun.Equal(expected) {
		t.Fatalf("restart shortened backoff: got %v want %v", restarted.states[0].NextRun, expected)
	}
}
