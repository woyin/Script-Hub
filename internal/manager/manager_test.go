package manager

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/script-hub-org/script-hub/internal/cache"
)

const testKey = "abcdefghijklmnopqrstuvwxyz0123456789abcdef"

func newTestHandler(t *testing.T, fetch FetchFunc) *Handler {
	t.Helper()
	root := t.TempDir()
	c, err := cache.New(filepath.Join(root, "cache"), 60, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{BackendKey: testKey, DataDir: filepath.Join(root, "manager"), CacheDir: filepath.Join(root, "cache"), Cache: c, Fetch: fetch, Timeout: time.Second * 3, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func request(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	text := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		text = string(b)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(text))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
func create(t *testing.T, h *Handler, kind string, spec Spec) string {
	t.Helper()
	r := request(t, h, "POST", "/manage/"+testKey+"/api/"+kind, spec)
	if r.Code != 201 {
		t.Fatalf("create status %d: %s", r.Code, r.Body.String())
	}
	var v struct {
		ID string `json:"id"`
	}
	json.Unmarshal(r.Body.Bytes(), &v)
	return v.ID
}
func issue(t *testing.T, h *Handler, kind, id string) string {
	t.Helper()
	r := request(t, h, "POST", "/manage/"+testKey+"/api/"+kind+"/"+id+"/share", map[string]any{})
	if r.Code != 200 {
		t.Fatalf("share: %d %s", r.Code, r.Body.String())
	}
	var v map[string]string
	json.Unmarshal(r.Body.Bytes(), &v)
	return v["path"]
}
func TestBackendKeyAndOrigin(t *testing.T) {
	h := newTestHandler(t, nil)
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		r := request(t, h, method, "/manage/not-the-key/api/files", Spec{})
		if r.Code != 404 {
			t.Fatalf("unauthorized %s=%d", method, r.Code)
		}
	}
	r := request(t, h, "GET", "/manage/"+testKey+"/", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "订阅与文件管理") {
		t.Fatal("missing UI")
	}
	if r.Header().Get("Access-Control-Allow-Origin") != "" || r.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("unsafe management headers")
	}
	req := httptest.NewRequest("GET", "/manage/"+testKey+"/api/files", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	req = httptest.NewRequest("POST", "/manage/"+testKey+"/api/files", strings.NewReader(`{"name":"n","source":"local"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Fatal("non-JSON mutation accepted")
	}
}
func TestFileCRUDSharingAndCacheAuthorization(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "文本", Source: "local", Content: "<script>alert(1)</script>"})
	other := create(t, h, "files", Spec{Name: "other", Source: "local", Content: "private other"})
	path := issue(t, h, "files", id)
	for i := 0; i < 2; i++ {
		r := request(t, h, "GET", path, nil)
		if r.Code != 200 || r.Body.String() != "<script>alert(1)</script>" || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/plain") {
			t.Fatal("file output corrupted or served as executable HTML")
		}
		if i == 1 && r.Header().Get("X-ScriptHub-Cache") != "HIT" {
			t.Fatal("disk cache not used")
		}
	}
	wrong := strings.Replace(path, id, other, 1)
	if r := request(t, h, "GET", wrong, nil); r.Code != 404 {
		t.Fatal("token used for another resource")
	}
	r := request(t, h, "GET", path+"?refresh=true", nil)
	if r.Code != 403 {
		t.Fatal("share force refresh accepted")
	}
	fresh := issue(t, h, "files", id)
	if r := request(t, h, "GET", path, nil); r.Code != 404 {
		t.Fatal("old token still authorizes cached content")
	}
	r = request(t, h, "GET", "/manage/"+testKey+"/api/files/"+id, nil)
	if strings.Contains(r.Body.String(), "shareDigest") || strings.Contains(r.Body.String(), strings.Split(fresh, "/")[4]) {
		t.Fatal("share secret leaked in record")
	}
	r = request(t, h, "PUT", "/manage/"+testKey+"/api/files/"+id, Spec{Name: "updated", Source: "local", Content: "new"})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request(t, h, "GET", fresh, nil); r.Code != 200 || r.Body.String() != "new" {
		t.Fatal("updated content did not invalidate cache")
	}
	request(t, h, "DELETE", "/manage/"+testKey+"/api/files/"+id+"/share", nil)
	if r := request(t, h, "GET", fresh, nil); r.Code != 404 {
		t.Fatal("revocation did not revoke cached export")
	}
	record, err := h.store.Get("files", id)
	if err != nil || record.ShareDigest != "" {
		t.Fatal("revoke not persisted")
	}
	if r := request(t, h, "DELETE", "/manage/"+testKey+"/api/files/"+id, nil); r.Code != 204 {
		t.Fatal(r.Body.String())
	}
	if r := request(t, h, "GET", "/manage/"+testKey+"/api/files/"+id, nil); r.Code != 404 {
		t.Fatal("deleted resource remains")
	}
}
func TestPersistenceQuotaAndSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 5000)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Save("files", "", Spec{Name: "old", Source: "local", Content: "old"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Share("files", r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.path(r.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), token) {
		t.Fatal("plaintext token on disk")
	}
	if _, err := s.Save("files", r.ID, Spec{Name: "new", Source: "local", Content: strings.Repeat("x", 5000)}); !errors.Is(err, ErrLimit) {
		t.Fatalf("quota: %v", err)
	}
	restarted, err := NewStore(dir, 5000)
	if err != nil {
		t.Fatal(err)
	}
	old, err := restarted.Get("files", r.ID)
	if err != nil || old.Spec.Content != "old" || !CheckShare(old, token) {
		t.Fatal("restart/failed replacement lost old data")
	}
	info, _ := os.Stat(s.path(r.ID))
	if info.Mode().Perm() != 0600 {
		t.Fatal("resource file is not private")
	}
	if _, err := s.Get("files", "../outside"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unsafe resource id accepted")
	}
}
func TestRemoteFailurePreservesCachedResult(t *testing.T) {
	var failure atomic.Bool
	var count atomic.Int64
	h := newTestHandler(t, func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		count.Add(1)
		io.WriteString(w, "complete")
		if failure.Load() {
			return errors.New("https://secret-url?token=private")
		}
		return nil
	})
	id := create(t, h, "files", Spec{Name: "remote", Source: "remote", URLs: []string{"https://source.example/data"}})
	path := "/manage/" + testKey + "/api/files/" + id + "/export"
	if r := request(t, h, "GET", path, nil); r.Code != 200 || r.Body.String() != "complete" {
		t.Fatal("initial export failed")
	}
	failure.Store(true)
	r := request(t, h, "GET", path+"?refresh=true", nil)
	if r.Code != 422 || strings.Contains(r.Body.String(), "private") || strings.Contains(r.Body.String(), "complete") {
		t.Fatalf("partial/secret response: %d %s", r.Code, r.Body.String())
	}
	if r := request(t, h, "GET", path, nil); r.Code != 200 || r.Body.String() != "complete" || r.Header().Get("X-ScriptHub-Cache") != "HIT" {
		t.Fatal("failure replaced old cache")
	}
	if count.Load() != 2 {
		t.Fatal("unexpected fetch count")
	}
}
func TestSubscriptionMergeAndClientExports(t *testing.T) {
	h := newTestHandler(t, func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		if !strings.HasPrefix(u, "https://source.example/") {
			return errors.New("unexpected")
		}
		name := "one"
		if strings.HasSuffix(u, "/two") {
			name = "two"
		}
		io.WriteString(w, "proxies:\n - name: "+name+"\n   type: ss\n   server: x\n   port: 443\n   cipher: aes-128-gcm\n   password: secret\n")
		return nil
	})
	id := create(t, h, "subscriptions", Spec{Name: "merged", Source: "remote", URLs: []string{"https://source.example/one", "https://source.example/two"}})
	for _, target := range []string{"clash", "shadowrocket", "loon", "egern"} {
		r := request(t, h, "GET", "/manage/"+testKey+"/api/subscriptions/"+id+"/export?target="+target, nil)
		if r.Code != 200 || r.Body.Len() == 0 {
			t.Fatalf("%s: %d %s", target, r.Code, r.Body.String())
		}
	}
	path := issue(t, h, "subscriptions", id)
	if r := request(t, h, "GET", path+"?target=egern", nil); r.Code != 200 || !strings.Contains(r.Body.String(), "shadowsocks:") {
		t.Fatal("share export failed")
	}
}
func TestConcurrentExportsCoalesceAndCancellation(t *testing.T) {
	var count atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	h := newTestHandler(t, func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		count.Add(1)
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			_, err := io.WriteString(w, "remote")
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	id := create(t, h, "files", Spec{Name: "remote", Source: "remote", URLs: []string{"https://source.example/data"}})
	path := "/manage/" + testKey + "/api/files/" + id + "/export"
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := request(t, h, "GET", path, nil)
		if r.Code != 200 {
			t.Errorf("leader: %d", r.Code)
		}
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	cancel()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 408 {
		t.Fatalf("cancelled wait=%d", rec.Code)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := request(t, h, "GET", path, nil); r.Code != 200 || r.Body.String() != "remote" {
				t.Errorf("waiter: %d", r.Code)
			}
		}()
	}
	close(release)
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("same-key fetches=%d", count.Load())
	}
}
func TestUnsafeConfigurationAndSpec(t *testing.T) {
	for _, key := range []string{"short", strings.Repeat("x", 32) + "/path"} {
		if _, err := New(Config{BackendKey: key, DataDir: t.TempDir()}); err == nil {
			t.Fatal("weak/path key accepted")
		}
	}
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	os.Mkdir(cacheDir, 0700)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(cacheDir, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{BackendKey: testKey, DataDir: alias, CacheDir: cacheDir}); err == nil {
		t.Fatal("symlink overlap with cache directory accepted")
	}
	for _, spec := range []Spec{{Name: "x", Source: "remote", URLs: []string{"file:///etc/passwd"}}, {Name: "x", Source: "remote", URLs: []string{"https://u:p@host"}}, {Name: "x", Source: "remote", URLs: []string{"https://host"}, Headers: map[string]string{"X-Test": "a\r\nb"}}} {
		if err := ValidateSpec("files", spec); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
}
