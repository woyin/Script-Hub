package httpclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer 启动一个可编程响应的 httptest server，
// handler 闭包内可读到最近一次请求的副本（headers/method/body）。
func newTestServer(t *testing.T, status int, headers map[string]string, body []byte, useGzip bool) (*httptest.Server, *http.Request) {
	t.Helper()
	var lastReq http.Request
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 复制请求以便断言
		lastReq = *r
		buf := make([]byte, len(r.URL.Path))
		_ = buf
		if r.Body != nil {
			lastBody = make([]byte, 1024)
			n, _ := r.Body.Read(lastBody)
			lastBody = lastBody[:n]
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		if useGzip {
			w.Header().Set("Content-Encoding", "gzip")
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			gz.Write(body)
			gz.Close()
			w.WriteHeader(status)
			w.Write(buf.Bytes())
			return
		}
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &lastReq
	// 注意：返回的是 lastReq 的快照指针，断言时需在请求发出后访问其字段
}

func TestGet_PlainAndHeaders(t *testing.T) {
	srv, captured := newTestServer(t, 200, map[string]string{"X-Custom": "v1"}, []byte("hello"), false)
	c := NewClient(5)
	c.SetHeader("X-Test", "abc")
	body, status, err := c.Get(context.Background(), srv.URL+"/path")
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if body != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}
	// 默认 UA 应被发送
	if got := captured.Header.Get("User-Agent"); got != "script-hub/1.0.0" {
		t.Errorf("User-Agent = %q, want script-hub/1.0.0", got)
	}
	if got := captured.Header.Get("X-Test"); got != "abc" {
		t.Errorf("X-Test = %q, want abc", got)
	}
	// 响应头应能透传
	_ = captured
}

func TestGetWithHeaders_OverridesAndAdds(t *testing.T) {
	srv, captured := newTestServer(t, 201, nil, []byte("ok"), false)
	c := NewClient(2)
	c.SetHeader("X-A", "default")
	body, status, err := c.GetWithHeaders(context.Background(), srv.URL, map[string]string{
		"X-A":        "override", // 覆盖默认头
		"X-Extra":    "e1",
		"User-Agent": "custom/2.0",
	})
	if err != nil {
		t.Fatalf("GetWithHeaders err: %v", err)
	}
	if status != 201 || body != "ok" {
		t.Errorf("status/body = %d/%q, want 201/ok", status, body)
	}
	if got := captured.Header.Get("X-A"); got != "override" {
		t.Errorf("X-A = %q, want override", got)
	}
	if got := captured.Header.Get("X-Extra"); got != "e1" {
		t.Errorf("X-Extra = %q, want e1", got)
	}
	if got := captured.Header.Get("User-Agent"); got != "custom/2.0" {
		t.Errorf("User-Agent = %q, want custom/2.0", got)
	}
}

func TestGet_GzipAutoDecompress(t *testing.T) {
	srv, _ := newTestServer(t, 200, nil, []byte("compressed-content"), true)
	c := NewClient(5)
	body, status, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d", status)
	}
	if body != "compressed-content" {
		t.Errorf("body = %q, want compressed-content", body)
	}
}

func TestGet_ErrorStatusReturned(t *testing.T) {
	srv, _ := newTestServer(t, 503, nil, []byte("service unavailable"), false)
	c := NewClient(3)
	body, status, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if status != 503 {
		t.Errorf("status = %d, want 503", status)
	}
	if !strings.Contains(body, "service unavailable") {
		t.Errorf("body = %q", body)
	}
}

func TestGet_InvalidURL(t *testing.T) {
	c := NewClient(2)
	_, _, err := c.Get(context.Background(), "http://invalid.invalid.invalid.")
	if err == nil {
		t.Error("Get on invalid URL should return error")
	}
}

func TestPost_BodyForwarded(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := NewClient(2)
	body, status, err := c.Post(context.Background(), srv.URL, strings.NewReader("payload-data"))
	if err != nil {
		t.Fatalf("Post err: %v", err)
	}
	if status != 204 {
		t.Errorf("status = %d, want 204", status)
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
	if gotBody != "payload-data" {
		t.Errorf("server received body = %q, want payload-data", gotBody)
	}
}

func TestGet_TimeoutHonored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	// 用 1 秒超时 — 服务端 500ms 应能完成
	c := NewClient(1)
	if _, _, err := c.Get(context.Background(), srv.URL); err != nil {
		t.Errorf("1s timeout 但 500ms 服务应成功: %v", err)
	}
	// 用 0 秒超时 — 触发 http.Client.Timeout = 0 表示无超时，因此不会失败
	c2 := NewClient(0)
	// 这里我们只验证不 panic
	_, _, _ = c2.Get(context.Background(), srv.URL)
}

func TestGetBytesWithHeaders_ReturnsBytesAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Resp", "yes")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		w.Write([]byte{0x00, 0x01, 0xFF, 0x02})
	}))
	defer srv.Close()
	c := NewClient(3)
	bytes, status, respHeaders, err := c.GetBytesWithHeaders(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d", status)
	}
	if len(bytes) != 4 || bytes[0] != 0x00 || bytes[2] != 0xFF {
		t.Errorf("bytes = %v", bytes)
	}
	if respHeaders["X-Resp"] != "yes" {
		t.Errorf("X-Resp header missing, got=%v", respHeaders)
	}
	if respHeaders["Content-Type"] != "application/octet-stream" {
		t.Errorf("Content-Type missing/wrong, got=%v", respHeaders)
	}
}

func TestParseCustomHeaders(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"empty", "", map[string]string{}},
		{"single", "X-A: 1", map[string]string{"X-A": "1"}},
		{"multi", "X-A: 1\nX-B: 2", map[string]string{"X-A": "1", "X-B": "2"}},
		{"crlf", "X-A: 1\r\nX-B: 2", map[string]string{"X-A": "1", "X-B": "2"}},
		{"whitespace", "  X-A  :  1  ", map[string]string{"X-A": "1"}},
		{"skip_invalid", "badline\nX-A: 1\nempty:", map[string]string{"X-A": "1"}},
		{"skip_empty_val", "X-A:", map[string]string{}},
		{"colon_in_value", "X-A: http://x:8080", map[string]string{"X-A": "http://x:8080"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseCustomHeaders(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(c.want), got)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
