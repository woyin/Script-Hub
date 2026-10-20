package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func compatData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var v struct {
		Status string         `json:"status"`
		Data   map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &v) != nil || v.Status != "success" {
		t.Fatal("invalid compatibility envelope")
	}
	return v.Data
}
func TestSubStorePublishingContract(t *testing.T) {
	h := newTestHandler(t, nil)
	base := "/" + testKey
	sub := map[string]any{"name": "provider-raw", "displayName": "Provider RAW", "source": "local", "content": "trojan://password@proxy.example:443#test", "process": []any{}, "icon": "https://example.com/icon.png"}
	created := request(t, h, "POST", base+"/api/subs", sub)
	if created.Code != 201 {
		t.Fatalf("create subscription = %d", created.Code)
	}
	if compatData(t, created.Body.Bytes())["name"] != "provider-raw" {
		t.Fatal("name lost")
	}
	list := request(t, h, "GET", base+"/api/subs", nil)
	var rows struct {
		Status string
		Data   []map[string]any
	}
	if json.Unmarshal(list.Body.Bytes(), &rows) != nil || rows.Status != "success" || len(rows.Data) != 1 {
		t.Fatal("list contract")
	}
	read := request(t, h, "GET", base+"/api/sub/provider-raw", nil)
	if compatData(t, read.Body.Bytes())["content"] != sub["content"] {
		t.Fatal("read content")
	}
	patched := request(t, h, "PATCH", base+"/api/sub/provider-raw", map[string]any{"content": "trojan://newpass@proxy.example:443#test"})
	if patched.Code != 200 {
		t.Fatalf("patch = %d", patched.Code)
	}
	got := compatData(t, patched.Body.Bytes())
	if got["displayName"] != sub["displayName"] || got["icon"] != sub["icon"] || got["content"] == sub["content"] {
		t.Fatal("PATCH did not merge fields")
	}
	for _, target := range []string{"ClashMeta", "ShadowRocket", "Loon", "Egern"} {
		if r := request(t, h, "GET", base+"/download/provider-raw?target="+target, nil); r.Code != 200 {
			t.Fatal("download contract", target, r.Code)
		}
	}
	file := map[string]any{"name": "client-config", "displayName": "Client", "source": "local", "content": "[Rule]\nFINAL,DIRECT\n", "type": "text", "process": []any{}}
	if r := request(t, h, "POST", base+"/api/files", file); r.Code != 201 {
		t.Fatal("file create", r.Code)
	}
	r := request(t, h, "GET", base+"/api/wholeFile/client-config", nil)
	if compatData(t, r.Body.Bytes())["content"] != file["content"] {
		t.Fatal("wholeFile contract")
	}
	r = request(t, h, "GET", base+"/api/files", nil)
	rows.Data = nil
	rows.Status = ""
	if json.Unmarshal(r.Body.Bytes(), &rows) != nil || len(rows.Data) != 1 {
		t.Fatal("file list envelope")
	}
	if _, ok := rows.Data[0]["content"]; ok {
		t.Fatal("file list leaks full body")
	}
	r = request(t, h, "PATCH", base+"/api/file/client-config", map[string]any{"content": "updated\n"})
	if r.Code != 200 {
		t.Fatal("file patch", r.Code)
	}
	r = request(t, h, "GET", base+"/api/file/client-config", nil)
	if r.Code != 200 || r.Body.String() != "updated\n" {
		t.Fatal("file raw GET")
	}
	r = request(t, h, "DELETE", base+"/api/file/client-config", nil)
	if r.Code != 200 {
		t.Fatal("file delete", r.Code)
	}
	if r = request(t, h, "GET", base+"/api/wholeFile/client-config", nil); r.Code != 404 {
		t.Fatal("deleted file still accessible")
	}
}

func TestSubStoreAliasesNativeVisibilityAndProtection(t *testing.T) {
	h := newTestHandler(t, nil)
	id := create(t, h, "files", Spec{Name: "文本 文件", Source: "local", Content: "saved"})
	for i, base := range []string{"/" + testKey, "/manage/" + testKey} {
		expected := "saved"
		if i > 0 {
			expected = "changed"
		}
		r := request(t, h, "GET", base+"/api/wholeFile/"+id, nil)
		if r.Code != 200 || compatData(t, r.Body.Bytes())["content"] != expected {
			t.Fatal("native ID alias", r.Code)
		}
		r = request(t, h, "PATCH", base+"/api/file/"+id, map[string]any{"content": "changed"})
		if r.Code != 200 {
			t.Fatal("patch native resource", r.Code)
		}
	}
	r := request(t, h, "GET", "/manage/"+testKey+"/api/files/"+id, nil)
	if !strings.Contains(r.Body.String(), "changed") {
		t.Fatal("native did not see compatibility write")
	}
	for _, path := range []string{"/wrong/api/subs", "/api/subs", "/wrong/api/wholeFile/" + id} {
		if request(t, h, "GET", path, nil).Code != 404 {
			t.Fatal("unauthenticated access")
		}
	}
	req := request(t, h, http.MethodGet, "/manage/"+testKey+"/api/files", nil)
	var native []Summary
	if json.Unmarshal(req.Body.Bytes(), &native) != nil || len(native) != 1 {
		t.Fatal("native files interface changed")
	}
}

func TestSubStoreAtomicReplaceAndMetadataPersistence(t *testing.T) {
	h := newTestHandler(t, nil)
	base := "/" + testKey + "/api/"
	fileID := create(t, h, "files", Spec{Name: "unrelated", Source: "local", Content: "unchanged"})
	for _, name := range []string{"one", "two"} {
		r := request(t, h, "POST", base+"subs", map[string]any{"name": name, "source": "local", "content": "trojan://password@proxy.example:443#test", "displayName": "Display " + name})
		if r.Code != 201 {
			t.Fatal("create", r.Code)
		}
	}
	old, err := h.store.subStoreGet("subscriptions", "one")
	if err != nil {
		t.Fatal(err)
	}
	share := issue(t, h, "subscriptions", old.ID)
	old, _ = h.store.Get("subscriptions", old.ID)
	invalid := []map[string]any{{"name": "one", "source": "local", "content": "new"}, {"name": "bad/name", "source": "local", "content": "bad"}}
	if r := request(t, h, "PUT", base+"subs", invalid); r.Code != 400 {
		t.Fatal("accepted invalid replace", r.Code)
	}
	unchanged, _ := h.store.Get("subscriptions", old.ID)
	if revision(unchanged) != revision(old) {
		t.Fatal("partial replacement committed")
	}
	duplicate := []map[string]any{{"name": "one", "source": "local"}, {"name": "one", "source": "local"}}
	if r := request(t, h, "PUT", base+"subs", duplicate); r.Code != 400 {
		t.Fatal("duplicate replacement", r.Code)
	}
	valid := []map[string]any{{"name": "new", "source": "local", "content": "trojan://p@proxy.example:443#new"}, {"name": "one", "source": "local", "content": "trojan://p@proxy.example:443#one", "displayName": "Retained metadata"}}
	if r := request(t, h, "PUT", base+"subs", valid); r.Code != 200 {
		t.Fatal("replacement", r.Code)
	}
	current, _ := h.store.subStoreGet("subscriptions", "one")
	if current.ID != old.ID || current.ShareDigest != old.ShareDigest {
		t.Fatal("replacement changed retained identity or permission")
	}
	if r := request(t, h, "GET", share+"?target=egern", nil); r.Code != 200 {
		t.Fatal("retained share invalid", r.Code)
	}
	if _, err := h.store.subStoreGet("subscriptions", "two"); err != ErrNotFound {
		t.Fatal("removed row survived")
	}
	if r, _ := h.store.Get("files", fileID); r.Spec.Content != "unchanged" {
		t.Fatal("other kind changed")
	}
	rows, err := h.store.subStoreRecords("subscriptions")
	if err != nil || len(rows) != 2 || rows[0].Spec.Name != "new" || rows[1].Spec.Name != "one" {
		t.Fatal("bulk order changed")
	}
	// Native writes preserve flat API metadata; reloading and restoring do too.
	spec := current.Spec
	spec.Content = "trojan://p2@proxy.example:443#one"
	current, err = h.store.SaveVersion("subscriptions", current.ID, spec, revision(current))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := request(t, h2, "GET", base+"sub/one", nil)
	if compatData(t, r.Body.Bytes())["displayName"] != "Retained metadata" {
		t.Fatal("restart lost metadata")
	}
	backup, err := h2.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	reader, release, err := h2.store.openBackup(backup.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h2.store.Restore(reader); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	r = request(t, h2, "GET", base+"sub/one", nil)
	if compatData(t, r.Body.Bytes())["displayName"] != "Retained metadata" {
		t.Fatal("restore lost metadata")
	}
	if r = request(t, h2, "PUT", base+"subs", []any{}); r.Code != 200 {
		t.Fatal("empty replace", r.Code)
	}
	if rows, _ := h2.store.List("subscriptions"); len(rows) != 0 {
		t.Fatal("empty replacement left rows")
	}
	if _, err := h2.store.Get("files", fileID); err != nil {
		t.Fatal("empty replace deleted other kind")
	}
}

func TestSubStoreEncodedNamesPATCHAndUnsupportedFeatures(t *testing.T) {
	h := newTestHandler(t, nil)
	base := "/" + testKey + "/api/"
	payload := map[string]any{"name": "目录/配置 + 中文", "source": "local", "content": "before", "displayName": "Name", "process": []any{map[string]any{"type": "Script Operator", "disabled": true}}}
	r := request(t, h, "POST", base+"files", payload)
	if r.Code != 201 {
		t.Fatal("encoded name create", r.Code)
	}
	path := base + "file/" + url.PathEscape(payload["name"].(string))
	if r = request(t, h, "GET", path, nil); r.Code != 200 || r.Body.String() != "before" {
		t.Fatal("encoded path read", r.Code)
	}
	record, _ := h.store.subStoreGet("files", payload["name"].(string))
	etag := revision(record)
	if r = request(t, h, "PATCH", path, map[string]any{"name": "renamed", "content": "after"}); r.Code != 200 {
		t.Fatal("rename", r.Code)
	}
	if request(t, h, "GET", path, nil).Code != 404 {
		t.Fatal("old name survived rename")
	}
	if r := request(t, h, "DELETE", base+"file/renamed?mode=archive", nil); r.Code != 422 {
		t.Fatal("archive silently converted to permanent delete", r.Code)
	}
	r = request(t, h, "GET", base+"wholeFile/renamed?raw=true", nil)
	var raw map[string]any
	if json.Unmarshal(r.Body.Bytes(), &raw) != nil || raw["content"] != "after" || raw["status"] != nil {
		t.Fatal("raw document wrapped")
	}
	req := httptest.NewRequest("PATCH", base+"file/renamed", strings.NewReader(`{"content":"stale"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatal("explicit stale ETag accepted", rec.Code)
	}
	for _, body := range []any{nil, []any{}, map[string]any{"name": true}, map[string]any{"name": "x", "process": []any{map[string]any{"type": "Script Operator"}}}} {
		encoded, _ := json.Marshal(body)
		req = httptest.NewRequest("POST", base+"subs", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code < 400 {
			t.Fatal("invalid/unsupported payload accepted")
		}
		var v map[string]any
		if json.Unmarshal(rec.Body.Bytes(), &v) != nil || v["status"] != "failed" {
			t.Fatal("wrong failure envelope")
		}
	}
	if r = request(t, h, "POST", base+"files", map[string]any{"name": "renamed", "source": "local"}); r.Code != 400 {
		t.Fatal("duplicate name accepted")
	}
	req = httptest.NewRequest("DELETE", base+"file/renamed", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("DELETE incorrectly requires JSON body")
	}
}

func TestSubStoreConcurrentCreateAndRemoteHeaders(t *testing.T) {
	var calls atomic.Int32
	h := newTestHandler(t, func(ctx context.Context, u string, headers map[string]string, w io.Writer) error {
		calls.Add(1)
		if headers["User-Agent"] != "custom-agent" || headers["Authorization"] != "Bearer test" {
			t.Error("source headers not preserved")
		}
		_, err := io.WriteString(w, "remote text")
		return err
	})
	base := "/" + testKey + "/api/"
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := request(t, h, "POST", base+"files", map[string]any{"name": "same", "source": "local", "content": "x"})
			if r.Code == 201 {
				created.Add(1)
			} else if r.Code != 400 {
				t.Error("unexpected concurrent create status", r.Code)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatal("duplicate concurrent resources created")
	}
	r := request(t, h, "POST", base+"files", map[string]any{"name": "remote", "source": "remote", "url": "https://example.com/file", "userAgent": "custom-agent", "headers": map[string]string{"Authorization": "Bearer test"}})
	if r.Code != 201 {
		t.Fatal("remote create", r.Code)
	}
	r = request(t, h, "GET", base+"file/remote", nil)
	if r.Code != 200 || r.Body.String() != "remote text" || calls.Load() != 1 {
		t.Fatal("remote download", r.Code)
	}
	if r = request(t, h, "PATCH", base+"file/remote", map[string]any{"userAgent": nil}); r.Code != 200 {
		t.Fatal("clear UA")
	}
	native, _ := h.store.subStoreGet("files", "remote")
	if native.Spec.Headers["User-Agent"] != "" {
		t.Fatal("cleared UA retained")
	}
	r = request(t, h, "PATCH", base+"file/remote", map[string]any{"source": "local", "content": "local text"})
	if r.Code != 200 {
		t.Fatal("source switch", r.Code)
	}
	r = request(t, h, "GET", base+"file/remote", nil)
	if r.Body.String() != "local text" || calls.Load() != 1 {
		t.Fatal("local source fetched ignored URL")
	}
}
