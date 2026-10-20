package manager

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkResourceList4MiB(b *testing.B) {
	s, err := NewStore(filepath.Join(b.TempDir(), "manager"), 32<<20)
	if err != nil {
		b.Fatal(err)
	}
	for range 4 {
		if _, err = s.Save("files", "", Spec{Name: "large", Source: "local", Content: strings.Repeat("x", 1<<20)}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rows, err := s.List("files")
		if err != nil || len(rows) != 4 {
			b.Fatal(err)
		}
	}
}

func TestWarmExportDoesNotWaitForConversionSlots(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "warm", Source: "local", Content: "cached text"})
	r, _ := h.store.Get("files", id)
	rd, _, err := h.export(context.Background(), r, "text", false)
	closeBody(rd)
	if err != nil {
		t.Fatal(err)
	}
	for range cap(h.slots) {
		h.slots <- struct{}{}
	}
	defer func() {
		for len(h.slots) > 0 {
			<-h.slots
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/manage/"+testKey+"/api/files/"+id+"/export", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "cached text" {
		t.Fatalf("warm export queued behind conversion: %d %s", w.Code, w.Body.String())
	}
}

func TestFailedSourceRequestsHaveCooldown(t *testing.T) {
	h := newTestHandler(t, nil)
	var calls atomic.Int64
	h.cfg.Fetch = func(context.Context, string, map[string]string, io.Writer) error {
		calls.Add(1)
		return errors.New("upstream down")
	}
	for range 3 {
		if err := h.fetch(context.Background(), "https://example.com/fail", nil, io.Discard); err == nil {
			t.Fatal("expected source failure")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("repeated failed upstream calls: %d", calls.Load())
	}
}

func TestMetadataIndexAndCacheNeverReadLargeBodyOnHit(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "indexed", Source: "local", Content: strings.Repeat("x", 1<<20)})
	record, _ := h.store.Get("files", id)
	rd, _, err := h.export(context.Background(), record, "text", false)
	closeBody(rd)
	if err != nil {
		t.Fatal(err)
	}
	path := h.store.path(id)
	if err = os.Rename(path, path+".hidden"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(path+".hidden", path)
	rows, err := h.store.List("files")
	if err != nil || len(rows) != 1 || rows[0].Name != "indexed" {
		t.Fatal("list accessed resource body", err)
	}
	w := request(t, h, "GET", "/manage/"+testKey+"/api/files/"+id+"/export", nil)
	if w.Code != 200 || w.Body.Len() != 1<<20 {
		t.Fatal("hit read body document", w.Code)
	}
}
func TestIndexedShareRevocationWhileAllConversionsBusy(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "f", Source: "local", Content: "value"})
	path := issue(t, h, "files", id)
	record, _ := h.store.Get("files", id)
	rd, _, err := h.export(context.Background(), record, "text", false)
	closeBody(rd)
	if err != nil {
		t.Fatal(err)
	}
	for range cap(h.slots) {
		h.slots <- struct{}{}
	}
	defer func() {
		for len(h.slots) > 0 {
			<-h.slots
		}
	}()
	if w := request(t, h, "GET", path, nil); w.Code != 200 {
		t.Fatal("share hit blocked", w.Code)
	}
	if _, err = h.store.Share("files", id, true); err != nil {
		t.Fatal(err)
	}
	if w := request(t, h, "GET", path, nil); w.Code != 404 {
		t.Fatal("revoked share used cached body", w.Code)
	}
}
func TestSourceCooldownForceRecoveryAndIsolation(t *testing.T) {
	h := newTestHandler(t, nil)
	var calls atomic.Int64
	var failed atomic.Bool
	failed.Store(true)
	h.cfg.Fetch = func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		calls.Add(1)
		if failed.Load() {
			return errors.New("down")
		}
		_, err := io.WriteString(w, testNodes)
		return err
	}
	url := "https://example.com/s"
	for range 2 {
		_ = h.fetch(context.Background(), url, nil, io.Discard)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	_ = h.fetch(context.Background(), url, map[string]string{"Authorization": "other"}, io.Discard)
	if calls.Load() != 2 {
		t.Fatal("credential cooldown leaked")
	}
	ctx := context.WithValue(context.Background(), refreshSourceKey{}, time.Now())
	failed.Store(false)
	if err := h.fetch(ctx, url, nil, io.Discard); err != nil || calls.Load() != 3 {
		t.Fatal("force did not recover", err)
	}
	h.sourceMu.Lock()
	_, cooling := h.sourceFailures[sourceKey(url, nil)]
	h.sourceMu.Unlock()
	if cooling {
		t.Fatal("successful force retained failure cooldown")
	}
	failed.Store(true)
	different := "https://example.com/other"
	_ = h.fetch(context.Background(), different, nil, io.Discard)
	h.sourceMu.Lock()
	h.sourceFailures[sourceKey(different, nil)] = time.Now().Add(-time.Second)
	h.sourceMu.Unlock()
	_ = h.fetch(context.Background(), different, nil, io.Discard)
	if calls.Load() != 5 {
		t.Fatal("expired cooldown not retried", calls.Load())
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_ = h.fetch(cancelled, "https://example.com/cancel", nil, io.Discard)
	if calls.Load() != 5 {
		t.Fatal("cancelled request called upstream")
	}
}
func TestOverlappingTargetsShareOnlyActivePreparedNodes(t *testing.T) {
	h := newTestHandler(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	h.cfg.Fetch = func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		_, err := io.WriteString(w, testNodes)
		return err
	}
	id := create(t, h, "subscriptions", Spec{Name: "s", Source: "remote", URLs: []string{"https://example.com/sub"}})
	record, _ := h.store.Get("subscriptions", id)
	results := make(chan error, 2)
	go func() {
		rd, _, err := h.export(context.Background(), record, "loon", false)
		closeBody(rd)
		results <- err
	}()
	<-entered
	go func() {
		rd, _, err := h.export(context.Background(), record, "egern", false)
		closeBody(rd)
		results <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		h.prepMu.Lock()
		refs := 0
		for _, p := range h.preparations {
			refs = p.refs
		}
		h.prepMu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("second target did not join preparation")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	h.stats.mu.Lock()
	count := h.stats.stages["parse_process"].Count
	h.stats.mu.Unlock()
	if count != 1 || calls.Load() != 1 {
		t.Fatal("parsed more than once", count, calls.Load())
	}
	h.prepMu.Lock()
	left := len(h.preparations)
	h.prepMu.Unlock()
	if left != 0 {
		t.Fatal("nodes retained after task")
	}
	rd, _, err := h.export(context.Background(), record, "shadowrocket", false)
	closeBody(rd)
	if err != nil {
		t.Fatal(err)
	}
	h.stats.mu.Lock()
	count = h.stats.stages["parse_process"].Count
	h.stats.mu.Unlock()
	if count != 2 || calls.Load() != 1 {
		t.Fatal("inactive nodes reused or source downloaded again")
	}
}
func TestAutomaticBackupSkipsUnchangedAcrossRestart(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "manager"), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Save("files", "", Spec{Name: "f", Source: "local", Content: "one"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	created, err := s.SnapshotIfChanged(now)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	tomorrow := now.Add(24 * time.Hour)
	created, err = s.SnapshotIfChanged(tomorrow)
	if err != nil || created {
		t.Fatal("unchanged snapshot compressed", err)
	}
	status, _ := s.BackupStatus()
	if !status.CheckedAt.Equal(tomorrow) {
		t.Fatal("check time not saved")
	}
	if _, err = s.Save("files", r.ID, r.Spec); err != nil {
		t.Fatal(err)
	}
	created, err = s.SnapshotIfChanged(tomorrow.Add(24 * time.Hour))
	if err != nil || created {
		t.Fatal("timestamp-only save created duplicate", err)
	}
	r.Spec.Content = "two"
	if _, err = s.Save("files", r.ID, r.Spec); err != nil {
		t.Fatal(err)
	}
	created, err = s.SnapshotIfChanged(now.Add(3 * 24 * time.Hour))
	if err != nil || !created {
		t.Fatal("changed content not backed up", err)
	}
	if _, err = s.Share("files", r.ID, false); err != nil {
		t.Fatal(err)
	}
	created, err = s.SnapshotIfChanged(now.Add(4 * 24 * time.Hour))
	if err != nil || !created {
		t.Fatal("share permission change not backed up", err)
	}
	s, err = NewStore(s.root, s.maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	created, err = s.SnapshotIfChanged(now.Add(5 * 24 * time.Hour))
	if err != nil || created {
		t.Fatal("restart lost backup fingerprint", err)
	}
	rows, _ := s.Backups()
	if len(rows) != 3 {
		t.Fatal("duplicate snapshots", len(rows))
	}
	if _, err = s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Backups()
	if len(rows) != 4 {
		t.Fatal("manual snapshot was deduplicated")
	}
}
func TestMetadataMutationFailureAndRestoreRefreshIndex(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "before", Source: "local", Content: "body"})
	old, _ := h.store.Get("files", id)
	h.store.maxBytes = 1
	if _, err := h.store.Save("files", id, Spec{Name: "failed", Source: "local"}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	meta, _ := h.store.Meta("files", id)
	if meta.Summary.Name != "before" || revision(meta.record()) != revision(old) {
		t.Fatal("failed write changed index")
	}
	h.store.maxBytes = 32 << 20
	b, err := h.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(h.store.backupDir(), b.Name))
	if err != nil {
		t.Fatal(err)
	}
	if err = h.store.Delete("files", id); err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.Meta("files", id); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted index entry retained")
	}
	if _, err = h.store.Restore(strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	meta, err = h.store.Meta("files", id)
	if err != nil || meta.Summary.Name != "before" || revision(meta.record()) == revision(old) {
		t.Fatal("restore index stale", err)
	}
}

func TestCancelledWarmReadAndStreamBackpressureReleasePermits(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "f", Source: "local", Content: "warm"})
	r, _ := h.store.Get("files", id)
	rd, _, err := h.export(context.Background(), r, "text", false)
	closeBody(rd)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/manage/"+testKey+"/api/files/"+id+"/export", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 408 || len(h.readSlots) != 0 || len(h.slots) != 0 {
		t.Fatal("cancelled warm read consumed permits", w.Code)
	}
	for range cap(h.readSlots) {
		h.readSlots <- struct{}{}
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	req = httptest.NewRequest("GET", "/manage/"+testKey+"/api/files/"+id+"/export", nil).WithContext(ctx)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 504 || len(h.slots) != 0 {
		t.Fatal("stream overload used conversion permit", w.Code)
	}
	for len(h.readSlots) > 0 {
		<-h.readSlots
	}
}
