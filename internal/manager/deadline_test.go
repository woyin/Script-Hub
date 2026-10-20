package manager

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func waitString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled work was not woken")
		return ""
	}
}
func stopManager(t *testing.T, h *Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Stop(ctx); err != nil {
		t.Error(err)
	}
}
func scheduledSpec(url string) Spec {
	return Spec{Name: url, Source: "remote", URLs: []string{url}, Refresh: RefreshPolicy{Enabled: true, Targets: []string{"egern"}}}
}
func TestDeadlineSelectionReservesForegroundAndDrainsDueJobs(t *testing.T) {
	h := newTestHandler(t, nil)
	h.slots = make(chan struct{}, 3)
	now := time.Now().UTC()
	h.nextBackup = now.Add(24 * time.Hour)
	entered := make(chan string, 4)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.cfg.Fetch = func(ctx context.Context, url string, headers map[string]string, w io.Writer) error {
		entered <- url
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		_, err := io.WriteString(w, testNodes)
		return err
	}
	for i, url := range []string{"https://example.com/oldest", "https://example.com/second", "https://example.com/third"} {
		r, err := h.store.Save("subscriptions", "", scheduledSpec(url))
		if err != nil {
			t.Fatal(err)
		}
		if err = h.runtime.update(r, func(s *ResourceStatus) { s.NextRun = now.Add(time.Duration(i-3) * time.Minute) }); err != nil {
			t.Fatal(err)
		}
	}
	h.step(ctx, now)
	seen := map[string]bool{waitString(t, entered): true, waitString(t, entered): true}
	if !seen["https://example.com/oldest"] || !seen["https://example.com/second"] {
		close(release)
		h.jobs.Wait()
		t.Fatal("due jobs not selected by age", seen)
	}
	id := create(t, h, "files", Spec{Name: "foreground", Source: "local", Content: "front"})
	w := request(t, h, "GET", "/manage/"+testKey+"/api/files/"+id+"/export", nil)
	if w.Code != 200 {
		close(release)
		h.jobs.Wait()
		t.Fatal("background consumed foreground reserve", w.Code)
	}
	close(release)
	h.jobs.Wait()
	h.step(ctx, now)
	if got := waitString(t, entered); !strings.HasSuffix(got, "/third") {
		t.Fatal(got)
	}
	h.jobs.Wait()
	next := h.step(ctx, now)
	h.jobs.Wait()
	if !next.After(now.Add(5*time.Hour)) || !next.Before(now.Add(8*time.Hour)) {
		t.Fatal("nearest deadline incorrect", next)
	}
}
func TestSchedulerWakesForEnableCompletionAndDisable(t *testing.T) {
	h := newTestHandler(t, nil)
	h.nextBackup = time.Now().Add(24 * time.Hour)
	entered := make(chan string, 4)
	cancelled := make(chan string, 2)
	h.cfg.Fetch = func(ctx context.Context, url string, headers map[string]string, w io.Writer) error {
		entered <- url
		if strings.HasSuffix(url, "/cancel") {
			<-ctx.Done()
			cancelled <- url
			return ctx.Err()
		}
		_, err := io.WriteString(w, testNodes)
		return err
	}
	dormant := scheduledSpec("https://example.com/first")
	dormant.Refresh.Enabled = false
	r, err := h.store.Save("subscriptions", "", dormant)
	if err != nil {
		t.Fatal(err)
	}
	h.Start()
	defer stopManager(t, h)
	dormant.Refresh.Enabled = true
	if _, err = h.store.Save("subscriptions", r.ID, dormant); err != nil {
		t.Fatal(err)
	}
	if got := waitString(t, entered); !strings.HasSuffix(got, "/first") {
		t.Fatal(got)
	}
	// A newly enabled resource wakes immediately instead of waiting for a polling interval.
	if _, err = h.store.Save("subscriptions", "", scheduledSpec("https://example.com/second")); err != nil {
		t.Fatal(err)
	}
	if got := waitString(t, entered); !strings.HasSuffix(got, "/second") {
		t.Fatal(got)
	}
	active, err := h.store.Save("subscriptions", "", scheduledSpec("https://example.com/cancel"))
	if err != nil {
		t.Fatal(err)
	}
	if got := waitString(t, entered); !strings.HasSuffix(got, "/cancel") {
		t.Fatal(got)
	}
	active.Spec.Refresh.Enabled = false
	if _, err = h.store.Save("subscriptions", active.ID, active.Spec); err != nil {
		t.Fatal(err)
	}
	if got := waitString(t, cancelled); !strings.HasSuffix(got, "/cancel") {
		t.Fatal(got)
	}
}
func TestSchedulerNextDeadlineDoesNotExecuteFutureJobs(t *testing.T) {
	h := newTestHandler(t, nil)
	now := time.Now().UTC()
	h.nextBackup = now.Add(24 * time.Hour)
	r, err := h.store.Save("subscriptions", "", scheduledSpec("https://example.com/future"))
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(3 * time.Hour)
	if err = h.runtime.update(r, func(s *ResourceStatus) { s.NextRun = due }); err != nil {
		t.Fatal(err)
	}
	next := h.step(context.Background(), now)
	h.jobs.Wait()
	if !next.Equal(due) || len(h.slots) != 0 {
		t.Fatal("future work was executed or deadline missed", next)
	}
}
func TestSchedulerCompletesAllDueWithoutPollingDelay(t *testing.T) {
	h := newTestHandler(t, nil)
	h.nextBackup = time.Now().Add(24 * time.Hour)
	entered := make(chan string, 3)
	h.cfg.Fetch = func(ctx context.Context, url string, headers map[string]string, w io.Writer) error {
		entered <- url
		_, err := io.WriteString(w, testNodes)
		return err
	}
	for _, url := range []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"} {
		if _, err := h.store.Save("subscriptions", "", scheduledSpec(url)); err != nil {
			t.Fatal(err)
		}
	}
	h.Start()
	defer stopManager(t, h)
	seen := map[string]bool{}
	for range 3 {
		seen[waitString(t, entered)] = true
	}
	if len(seen) != 3 {
		t.Fatal("same job executed more than once before other due work", seen)
	}
}
