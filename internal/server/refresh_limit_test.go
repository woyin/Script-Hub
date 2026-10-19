package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
)

func TestPublicRefreshLimits(t *testing.T) {
	now := time.Now()
	l := newRefreshLimiter(60, 2)
	l.now = func() time.Time { return now }
	if l.allow("a") != 0 || l.allow("a") != time.Minute {
		t.Fatal("same key cooldown")
	}
	if l.allow("b") != 0 || l.allow("c") != time.Minute {
		t.Fatal("global rate")
	}
	now = now.Add(time.Minute)
	if l.allow("a") != 0 {
		t.Fatal("cooldown did not expire")
	}
}
func TestPublicRefreshHTTPAndPOSTShareLimit(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "DOMAIN,example.test") }))
	defer source.Close()
	cfg := config.LoadConfig()
	cfg.CacheDir = t.TempDir()
	srv := New(cfg)
	defer srv.Shutdown(context.Background())
	ts := httptest.NewServer(srv.router)
	defer ts.Close()
	payload := fmt.Sprintf(`{"type":"rule-set","target":"surge-rule-set","urls":[%q],"args":{"refresh":"true"}}`, source.URL)
	post := func() (int, string) {
		resp, err := http.Post(ts.URL+"/api/convert", "application/json", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header.Get("Retry-After")
	}
	if status, _ := post(); status != 200 {
		t.Fatalf("first refresh %d", status)
	}
	if status, retry := post(); status != 429 || retry == "" {
		t.Fatalf("repeat status %d retry %q", status, retry)
	}
	// 相同缓存键在 GET 和 POST 间共用冷却时间。
	path := "/file/_start_/" + url.PathEscape(source.URL) + "/_end_/?type=rule-set&target=surge-rule-set"
	resp, err := http.Get(ts.URL + path + "&refresh=true")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("GET refresh status %d", resp.StatusCode)
	}
	resp, err = http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-ScriptHub-Cache") != "HIT" {
		t.Fatal("ordinary cache read limited")
	}
}
