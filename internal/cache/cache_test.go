package cache

import (
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
	c := testCache(t, 60, WorkspaceBytes+1024)
	ws, err := c.Workspace("key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Workspace("other"); err == nil {
		t.Fatal("over-reservation accepted")
	}
	ws.limit = 10
	f, err := ws.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, strings.NewReader("12345678901")); err == nil {
		t.Fatal("ReadFrom bypassed quota")
	}
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("123")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ws.Close()
	if c.used != 0 {
		t.Fatalf("workspace reservation leaked %d", c.used)
	}
	// 模拟进程退出留下的旧格式缓存与临时目录。
	os.WriteFile(filepath.Join(c.dir, "legacy.json"), []byte("old"), 0600)
	os.Mkdir(filepath.Join(c.dir, ".work-crashed"), 0700)
	restarted, err := New(c.dir, 60, c.maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.used != 0 {
		t.Fatal("restart quota")
	}
	files, _ := os.ReadDir(c.dir)
	if len(files) != 0 {
		t.Fatal("startup leftovers")
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
