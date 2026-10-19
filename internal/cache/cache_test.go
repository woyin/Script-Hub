package cache

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/script-hub-org/script-hub/internal/types"
)

func testCache(t *testing.T, ttl int, budget int64) *Cache {
	t.Helper()
	c, err := New(t.TempDir(), ttl, budget)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func response(body string) types.ResponseData {
	return types.ResponseData{Status: 200, Headers: map[string]string{"Content-Type": "text/plain"}, Body: body}
}
func TestCache_Disabled(t *testing.T) {
	c, err := New("", 0, 0)
	if err != nil || c != nil {
		t.Fatalf("%v %v", c, err)
	}
	if _, ok := c.Get("x"); ok {
		t.Fatal("nil cache hit")
	}
	if err := c.Set("x", response("x")); err != nil {
		t.Fatal(err)
	}
}
func TestCache_PersistsAcrossRestart(t *testing.T) {
	c := testCache(t, 60, 0)
	if err := c.Set("key", response("original")); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(c.dir, 60, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get("key")
	if !ok {
		t.Fatal("cache miss")
	}
	defer got.File.Close()
	body, _ := io.ReadAll(got.File.Reader())
	if string(body) != "original" || got.Headers["Content-Type"] != "text/plain" {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := reopened.Get("other"); ok {
		t.Fatal("unknown hit")
	}
}
func TestCache_ExpiryAndCorruption(t *testing.T) {
	c := testCache(t, 60, 0)
	if err := os.WriteFile(c.path("expired"), []byte(`{"ExpiresAt":"2000-01-01T00:00:00Z","Response":{"Status":200,"Body":"old"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path("broken"), []byte("bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"expired", "broken"} {
		if _, ok := c.Get(key); ok {
			t.Fatal("invalid hit")
		}
		if _, err := os.Stat(c.path(key)); !os.IsNotExist(err) {
			t.Fatalf("invalid file not removed: %v", err)
		}
	}
}
func TestCache_CapacityAndFailedReplacement(t *testing.T) {
	c := testCache(t, 60, 600)
	if err := c.Set("a", response("a")); err != nil {
		t.Fatal(err)
	}
	// 确保 b 比 a 晚生成，容量不足时优先淘汰 a。
	old := time.Now().Add(-time.Second)
	os.Chtimes(c.path("a"), old, old)
	if err := c.Set("b", response(string(make([]byte, 30)))); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("c", response(string(make([]byte, 30)))); err != nil {
		t.Fatal(err)
	}
	if c.used > c.maxBytes {
		t.Fatal("capacity exceeded")
	}
	if rd, ok := c.Get("c"); !ok {
		t.Fatal("new entry missing")
	} else {
		rd.File.Close()
	}
	before, _ := os.ReadFile(c.path("c"))
	if err := c.Set("c", response(string(make([]byte, 1000)))); err == nil {
		t.Fatal("oversized accepted")
	}
	after, _ := os.ReadFile(c.path("c"))
	if string(before) != string(after) {
		t.Fatal("failed replacement changed old file")
	}
	files, _ := filepath.Glob(filepath.Join(c.dir, ".tmp-*"))
	if len(files) != 0 {
		t.Fatal("temporary file leaked")
	}
}
func TestCache_CleanupAndConcurrency(t *testing.T) {
	c := testCache(t, 60, 0)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Set("key", response("body")); err != nil {
				t.Error(err)
			}
			if rd, ok := c.Get("key"); ok {
				rd.File.Close()
			}
		}()
	}
	wg.Wait()
	old := time.Now().Add(-time.Hour)
	c.mu.Lock()
	c.files[c.path("key")].expires = old
	c.mu.Unlock()
	if err := c.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if c.used != 0 {
		t.Fatalf("used=%d", c.used)
	}
}
func TestCache_RejectIncompleteAndBadDirectory(t *testing.T) {
	c := testCache(t, 60, 0)
	rd := response("")
	rd.Incomplete = true
	if err := c.Set("key", rd); err == nil {
		t.Fatal("cached incomplete result")
	}
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, []byte("x"), 0600)
	if _, err := New(path, 60, 0); err == nil {
		t.Fatal("invalid directory accepted")
	}
}

func TestCache_OpenReadersSurviveReplacementAndCleanup(t *testing.T) {
	c := testCache(t, 60, 0)
	if err := c.Set("key", response("old")); err != nil {
		t.Fatal(err)
	}
	old, ok := c.Get("key")
	if !ok {
		t.Fatal("missing old")
	}
	if err := c.Set("key", response("new")); err != nil {
		t.Fatal(err)
	}
	fresh, ok := c.Get("key")
	if !ok {
		t.Fatal("missing new")
	}
	a, _ := io.ReadAll(old.File.Reader())
	b, _ := io.ReadAll(fresh.File.Reader())
	if string(a) != "old" || string(b) != "new" {
		t.Fatalf("mixed generations %q %q", a, b)
	}
	old.File.Close()
	fresh.File.Close()
	if c.used != c.files[c.path("key")].size {
		t.Fatal("retired reader quota leaked")
	}
	c.files[c.path("key")].expires = time.Now().Add(-time.Second)
	if _, ok := c.Get("key"); ok {
		t.Fatal("expired hit")
	}
	if c.used != 0 {
		t.Fatal("expired quota leaked")
	}
}

func TestWorkspaceQuotaAndRecovery(t *testing.T) {
	c := testCache(t, 60, 10)
	ws, err := c.Workspace("key")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	other, err := c.Workspace("other")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if c.used != 0 {
		t.Fatalf("empty workspaces charged %d bytes", c.used)
	}
	f, err := ws.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := io.Copy(f, strings.NewReader("12345678901")); err == nil {
		t.Fatal("ReadFrom bypassed quota")
	}
	if c.used != 0 {
		t.Fatalf("rejected write charged %d bytes", c.used)
	}
	if _, err := f.Write([]byte("123")); err != nil {
		t.Fatal(err)
	}
	if c.used != 3 {
		t.Fatalf("used=%d, want 3", c.used)
	}
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}
	if c.used != 0 {
		t.Fatalf("reset quota=%d", c.used)
	}
	f.Close()
	ws.Close()
	if c.used != 0 {
		t.Fatalf("workspace leaked %d", c.used)
	}
	os.WriteFile(filepath.Join(c.dir, "legacy.json"), []byte("old"), 0600)
	os.Mkdir(filepath.Join(c.dir, ".work-crashed"), 0700)
	restarted, err := New(c.dir, 60, c.maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.used != 0 {
		t.Fatal("restart quota")
	}
}

// 小任务不能因固定预留而互相排斥，所有已写字节才计入总预算。
func TestThirtyWorkspacesChargeActualBytes(t *testing.T) {
	c := testCache(t, 60, 1024)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		ws, err := c.Workspace(fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		defer ws.Close()
		f, err := ws.Create()
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.Write([]byte("1234567890")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.used != 300 {
		t.Fatalf("used=%d, want 300", c.used)
	}
}

func BenchmarkCacheStreamHit(b *testing.B) {
	for _, size := range []int{1 << 20, 10 << 20, 50 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			c, err := New(b.TempDir(), 86400, 0)
			if err != nil {
				b.Fatal(err)
			}
			if err := c.Set("key", response(strings.Repeat("a", size))); err != nil {
				b.Fatal(err)
			}
			buf := make([]byte, 64<<10)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rd, ok := c.Get("key")
				if !ok {
					b.Fatal("miss")
				}
				if _, err := io.CopyBuffer(io.Discard, rd.File.Reader(), buf); err != nil {
					b.Fatal(err)
				}
				rd.File.Close()
			}
		})
	}
}

func TestWorkspaceConcurrentCapacityAndOverwrite(t *testing.T) {
	c := testCache(t, 60, 1000)
	var wg sync.WaitGroup
	var workspaces []*Workspace
	for i := 0; i < 30; i++ {
		ws, err := c.Workspace(fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, ws)
		f, err := ws.Create()
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer f.Close()
			_, err := f.Write(make([]byte, 80))
			if err != nil && !errors.Is(err, ErrCapacity) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.used != 960 {
		t.Fatalf("used=%d, want 960", c.used)
	}
	for _, ws := range workspaces {
		ws.Close()
		ws.Close()
	}
	if c.used != 0 {
		t.Fatalf("leaked %d bytes", c.used)
	}
	ws, _ := c.Workspace("overwrite")
	f, _ := ws.Create()
	defer f.Close()
	if _, err := f.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if c.used != 5 {
		t.Fatalf("overwrite charged %d, want 5", c.used)
	}
	ws.Close()
	if _, err := f.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed write: %v", err)
	}
}

func TestCacheFailedPartialCopyPreservesOldAndReleasesBytes(t *testing.T) {
	c := testCache(t, 60, 1024)
	if err := c.Set("key", response("original")); err != nil {
		t.Fatal(err)
	}
	used := c.used
	source, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.WriteString("partial"); err != nil {
		t.Fatal(err)
	}
	rd := response("")
	rd.File = types.NewFileBody(source, 0, 100, nil)
	defer rd.File.Close()
	if err := c.Set("key", rd); err == nil {
		t.Fatal("short source accepted")
	}
	if c.used != used {
		t.Fatalf("partial copy quota=%d, want %d", c.used, used)
	}
	got, ok := c.Get("key")
	if !ok {
		t.Fatal("old result lost")
	}
	defer got.File.Close()
	body, err := io.ReadAll(got.File.Reader())
	if err != nil || string(body) != "original" {
		t.Fatalf("old result: %q %v", body, err)
	}
}
