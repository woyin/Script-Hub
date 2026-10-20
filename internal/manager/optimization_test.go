package manager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testNodes = "proxies:\n  - {name: HK, type: ss, server: node.example.com, port: 443, cipher: aes-128-gcm, password: pass}\n"

func TestSourceSharedAcrossTargetsAndConditionalRefresh(t *testing.T) {
	h := newTestHandler(t, nil)
	var calls atomic.Int64
	var conditional atomic.Int64
	h.cfg.FetchConditional = func(ctx context.Context, u string, headers map[string]string, w io.Writer) (int, http.Header, error) {
		calls.Add(1)
		if headers["If-None-Match"] == `"v1"` {
			conditional.Add(1)
			return 304, http.Header{"Etag": {`"v1"`}}, nil
		}
		time.Sleep(15 * time.Millisecond)
		_, err := io.WriteString(w, testNodes)
		return 200, http.Header{"Etag": {`"v1"`}}, err
	}
	id := create(t, h, "subscriptions", Spec{Name: "shared", Source: "remote", URLs: []string{"https://example.com/sub"}})
	var wg sync.WaitGroup
	for _, target := range []string{"shadowrocket", "loon", "egern"} {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			r := request(t, h, "GET", "/manage/"+testKey+"/api/subscriptions/"+id+"/export?target="+target, nil)
			if r.Code != 200 {
				t.Errorf("%s: %d %s", target, r.Code, r.Body.String())
			}
		}(target)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("downloads=%d", calls.Load())
	}
	r := request(t, h, "GET", "/manage/"+testKey+"/api/subscriptions/"+id+"/export?target=egern&refresh=true", nil)
	if r.Code != 200 || conditional.Load() != 1 {
		t.Fatalf("304 refresh: %d, conditional %d: %s", r.Code, conditional.Load(), r.Body.String())
	}
	var body bytes.Buffer
	if err := h.fetch(context.Background(), "https://example.com/sub", map[string]string{"Authorization": "different"}, &body); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("credentials must isolate source cache")
	}
	// Reopen the cache: source bytes and validators survive a process restart.
	h2, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	body.Reset()
	if err = h2.fetch(context.Background(), "https://example.com/sub", nil, &body); err != nil || body.String() != testNodes || calls.Load() != 3 {
		t.Fatalf("restart: %v calls %d", err, calls.Load())
	}
}
func TestStaleBoundedAndRevisionIsolated(t *testing.T) {
	h := newTestHandler(t, nil)
	h.cfg.CacheTTL = 15 * time.Millisecond
	h.cfg.StaleTTL = 180 * time.Millisecond
	var fail atomic.Bool
	h.cfg.Fetch = func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		if fail.Load() {
			return errors.New("secret https://example.com/private-token")
		}
		_, err := io.WriteString(w, testNodes)
		return err
	}
	id := create(t, h, "subscriptions", Spec{Name: "s", Source: "remote", URLs: []string{"https://example.com/sub"}})
	path := "/manage/" + testKey + "/api/subscriptions/" + id + "/export?target=egern"
	if r := request(t, h, "GET", path, nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	fail.Store(true)
	time.Sleep(25 * time.Millisecond)
	r := request(t, h, "GET", path, nil)
	if r.Code != 200 || r.Header().Get("X-ScriptHub-Cache") != "STALE" || r.Header().Get("X-ScriptHub-Stale") != "true" {
		t.Fatalf("stale=%d %s", r.Code, r.Body.String())
	}
	if r = request(t, h, "GET", path+"&refresh=true", nil); r.Code == 200 || strings.Contains(r.Body.String(), "private-token") {
		t.Fatal("force hid error or leaked source")
	}
	record, _ := h.store.Get("subscriptions", id)
	spec := record.Spec
	spec.URLs = []string{"https://example.com/new"}
	if _, err := h.store.Save("subscriptions", id, spec); err != nil {
		t.Fatal(err)
	}
	if r = request(t, h, "GET", path, nil); r.Code == 200 {
		t.Fatal("old revision leaked")
	}
	// Original key cannot be read after its hard retention boundary.
	time.Sleep(190 * time.Millisecond)
	if _, ok := h.cfg.Cache.GetRetained(outputKey(record, "egern")); ok {
		t.Fatal("unbounded stale retention")
	}
}
func TestFailedSourceFlightDoesNotReportOldFreshAsForceSuccess(t *testing.T) {
	h := newTestHandler(t, nil)
	h.cfg.Fetch = func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		_, err := io.WriteString(w, testNodes)
		return err
	}
	var initial bytes.Buffer
	if err := h.fetch(context.Background(), "https://example.com/s", nil, &initial); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	h.cfg.Fetch = func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		close(entered)
		<-release
		return errors.New("fail")
	}
	ctx := context.WithValue(context.Background(), refreshSourceKey{}, time.Now())
	results := make(chan error, 2)
	go func() { results <- h.fetch(ctx, "https://example.com/s", nil, io.Discard) }()
	<-entered
	go func() { results <- h.fetch(ctx, "https://example.com/s", nil, io.Discard) }()
	time.Sleep(15 * time.Millisecond)
	close(release)
	for range 2 {
		if err := <-results; err == nil {
			t.Fatal("failed force returned old fresh body")
		}
	}
}
func TestOptimisticWritesAndOldJobCannotPublish(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "f", Source: "local", Content: "old"})
	old, _ := h.store.Get("files", id)
	next, err := h.store.SaveVersion("files", id, Spec{Name: "f", Source: "local", Content: "new"}, revision(old))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.SaveVersion("files", id, old.Spec, revision(old)); !errors.Is(err, ErrConflict) {
		t.Fatal("lost-update protection", err)
	}
	req := httptest.NewRequest("PUT", "/manage/"+testKey+"/api/files/"+id, strings.NewReader(`{"name":"f","source":"local","content":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", revision(old))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = h.produce(context.Background(), old, outputKey(old, "text"), "text"); !errors.Is(err, ErrConflict) {
		t.Fatal("outdated job published", err)
	}
	current, _ := h.store.Get("files", id)
	if revision(current) != revision(next) || current.Spec.Content != "new" {
		t.Fatal("resource changed")
	}
}
func TestSchedulerSnapshotsAllTargetsAndPersistsNextRun(t *testing.T) {
	h := newTestHandler(t, nil)
	var calls atomic.Int64
	h.cfg.Fetch = func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		calls.Add(1)
		_, err := io.WriteString(w, testNodes)
		return err
	}
	id := create(t, h, "subscriptions", Spec{Name: "auto", Source: "remote", URLs: []string{"https://example.com/sub"}, Refresh: RefreshPolicy{Enabled: true}})
	now := time.Now().UTC()
	h.step(context.Background(), now)
	record, _ := h.store.Get("subscriptions", id)
	status := h.runtime.get(record)
	if len(status.Targets) != 3 || status.LastSuccess.IsZero() || !status.NextRun.After(now.Add(5*time.Hour)) || calls.Load() != 1 {
		t.Fatalf("status=%+v calls=%d", status, calls.Load())
	}
	h.step(context.Background(), now.Add(time.Minute))
	if calls.Load() != 1 {
		t.Fatal("refreshed before due")
	}
	restarted, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.runtime.get(record).NextRun.Equal(status.NextRun) {
		t.Fatal("schedule lost on restart")
	}
	h.cfg.Fetch = func(context.Context, string, map[string]string, io.Writer) error { return errors.New("fail") }
	h.step(context.Background(), status.NextRun.Add(time.Second))
	status = h.runtime.get(record)
	if status.Failures != 1 || status.Error == "" || status.NextRun.Before(now.Add(5*time.Hour)) {
		t.Fatal("retry state missing", status)
	}
	h.Start()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = h.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestBackupRestoreAtomicAndPersistent(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "f", Source: "local", Content: "original"})
	sharePath := issue(t, h, "files", id)
	token := sharePath[strings.LastIndex(sharePath, "/")+1:]
	original, _ := h.store.Get("files", id)
	backup, err := h.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.store.backupDir(), backup.Name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.Save("files", id, Spec{Name: "f", Source: "local", Content: "edited"}); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), raw...)
	corrupt[len(corrupt)-5] ^= 0xff
	if _, err = h.store.Restore(bytes.NewReader(corrupt)); err == nil {
		t.Fatal("accepted checksum failure")
	}
	v, _ := h.store.Get("files", id)
	if v.Spec.Content != "edited" {
		t.Fatal("failed restore changed data")
	}
	count, err := h.store.Restore(bytes.NewReader(raw))
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	v, _ = h.store.Get("files", id)
	if v.Spec.Content != "original" || !CheckShare(v, token) || revision(v) == revision(original) {
		t.Fatal("restore lost identity/share or reused revision")
	}
	reopened, err := NewStore(h.store.root, h.store.maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	v, err = reopened.Get("files", id)
	if err != nil || v.Spec.Content != "original" {
		t.Fatal("generation not durable", err)
	}
	for range 7 {
		if _, err = reopened.Snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	list, err := reopened.Backups()
	if err != nil || len(list) != backupCount {
		t.Fatal(len(list), err)
	}
	var size int64
	for _, b := range list {
		size += b.Bytes
		info, _ := os.Stat(filepath.Join(reopened.backupDir(), b.Name))
		if info.Mode().Perm() != 0600 {
			t.Fatal("backup permissions")
		}
	}
	if size > backupLimit {
		t.Fatal("backup quota exceeded")
	}
}
func TestRestoreRejectsTraversal(t *testing.T) {
	h := newTestHandler(t, nil)
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../escape.json", Mode: 0600, Size: 2})
	tw.Write([]byte(`{}`))
	tw.Close()
	gz.Close()
	if _, err := h.store.Restore(&b); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestAdminBackupEndpointsAndMissingWriteVersion(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "secret", Source: "local", Content: "private data"})
	base := "/manage/" + testKey + "/api/"
	created := request(t, h, "POST", base+"backups", map[string]any{})
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var b BackupInfo
	if err := json.Unmarshal(created.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	download := request(t, h, "GET", base+"backups/"+b.Name, nil)
	if download.Code != 200 || download.Header().Get("Content-Type") != "application/gzip" {
		t.Fatal("backup download")
	}
	if err := h.store.Delete("files", id); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", base+"restore", bytes.NewReader(download.Body.Bytes()))
	req.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	v, err := h.store.Get("files", id)
	if err != nil || v.Spec.Content != "private data" {
		t.Fatal("HTTP restore failed", err)
	}
	for _, path := range []string{"status", "metrics", "backups", "backups/" + b.Name} {
		r := request(t, h, "GET", "/manage/wrong/api/"+path, nil)
		if r.Code != 404 {
			t.Fatal("unauthorized", path, r.Code)
		}
	}
	req = httptest.NewRequest("PUT", base+"files/"+id, strings.NewReader(`{"name":"f","source":"local"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 428 {
		t.Fatal("unconditional PUT accepted", w.Code)
	}
	metrics := request(t, h, "GET", base+"metrics", nil)
	if strings.Contains(metrics.Body.String(), "private data") {
		t.Fatal("metrics leaked private content")
	}
}

func TestBackupRotationProtectsActiveDownloads(t *testing.T) {
	h := newTestHandler(t, nil)
	create(t, h, "files", Spec{Name: "f", Source: "local", Content: "value"})
	for range backupCount {
		if _, err := h.store.Snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := h.store.Backups()
	var releases []func()
	for _, b := range list {
		_, release, err := h.store.openBackup(b.Name)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := h.store.Snapshot(); !errors.Is(err, ErrLimit) {
		t.Fatal("rotated pinned backups", err)
	}
	for _, release := range releases {
		release()
	}
	if _, err := h.store.Snapshot(); err != nil {
		t.Fatal(err)
	}
	list, _ = h.store.Backups()
	if len(list) != backupCount {
		t.Fatal("rotation count", len(list))
	}
}

func TestForceJoiningOrdinaryMissMustRevalidateSource(t *testing.T) {
	h := newTestHandler(t, nil)
	var calls atomic.Int64
	h.cfg.Fetch = func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		calls.Add(1)
		_, err := io.WriteString(w, testNodes)
		return err
	}
	id := create(t, h, "subscriptions", Spec{Name: "s", Source: "remote", URLs: []string{"https://example.com/s"}})
	record, _ := h.store.Get("subscriptions", id)
	if err := h.fetch(context.Background(), record.Spec.URLs[0], nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	h.store.mu.Lock()
	results := make(chan error, 2)
	go func() {
		rd, _, err := h.export(context.Background(), record, "egern", false)
		closeBody(rd)
		results <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		running := h.flights[outputKey(record, "egern")] != nil
		h.mu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			h.store.mu.Unlock()
			t.Fatal("flight not started")
		}
		time.Sleep(time.Millisecond)
	}
	go func() {
		rd, _, err := h.export(context.Background(), record, "egern", true)
		closeBody(rd)
		results <- err
	}()
	time.Sleep(15 * time.Millisecond)
	h.store.mu.Unlock()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("forced request reused ordinary cached source: fetches=%d", calls.Load())
	}
}
