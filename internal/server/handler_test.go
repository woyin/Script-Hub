package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/types"
)

// newTestServer 构造一个挂载真实路由的 Server 并启动 httptest 实例。
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := New(config.LoadConfig())
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)
	return ts
}

// fileURL 构造一个 /file/_start_/{url}/_end_/?{query} 形式的请求路径，
// 其中 url 默认是 http://local.text（这样解析器会用 localtext 参数作为正文，
// 无需实际网络请求）。
func fileURL(srcType, target, localText string) string {
	encoded := url.PathEscape("http://local.text")
	q := url.Values{}
	q.Set("type", srcType)
	if target != "" {
		q.Set("target", target)
	}
	if localText != "" {
		q.Set("localtext", localText)
	}
	// 注意：handler 的 ParseQueryString 用 url.PathUnescape（不解码 + 为空格），
	// 与 JS decodeURIComponent 行为一致。因此空格必须用 %20 而非 + 编码。
	// url.Values.Encode() 会把空格编成 +，所以这里手动替换。
	return "/file/_start_/" + encoded + "/_end_/?" + strings.ReplaceAll(q.Encode(), "+", "%20")
}

// ── 健康检查与首页 ──

func TestHealthz(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestVersion(t *testing.T) {
	// Set a known version, then verify /version returns it.
	config.SetVersion("test-1.2.3")
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/version")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "test-1.2.3" {
		t.Errorf("body = %q, want %q", string(body), "test-1.2.3")
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestRoot_ServesHTML(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	// HTML 应包含一些已知前端元素
	if !strings.Contains(string(body), "script") && !strings.Contains(string(body), "Script") {
		t.Errorf("body does not contain 'script': %q", string(body)[:min(200, len(body))])
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS missing")
	}
}

func TestUnknownRoute_404(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestFileHandler_UnknownType_400(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/file/_start_/x/_end_/?type=banana")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// ── 重写解析端点 ──

func TestRewriteParser_QXRewriteToSurge(t *testing.T) {
	ts := newTestServer(t)
	// 一条最简单的 QX rewrite：拒绝匹配 ^ads.example.com 的请求
	localText := "^https?://ads\\.example\\.com url reject"
	resp, err := http.Get(ts.URL + fileURL("qx-rewrite", "surge-module", localText))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	bodyStr := string(body)
	// Surge module 应包含 [URL Rewrite] 段
	if !strings.Contains(bodyStr, "URL Rewrite") && !strings.Contains(bodyStr, "reject") {
		t.Errorf("Surge output missing URL Rewrite / reject:\n%s", bodyStr)
	}
}

func TestRewriteParser_AllModuleDetect(t *testing.T) {
	ts := newTestServer(t)
	// all-module 应能自动识别 QX 格式
	localText := "^https?://x\\.com url reject"
	resp, err := http.Get(ts.URL + fileURL("all-module", "loon-plugin", localText))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
}

func TestRewriteParser_EmptyLocalText_OKEmpty(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + fileURL("qx-rewrite", "surge-module", ""))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// ── 规则集解析端点 ──

func TestRuleParser_SurgeRuleSet(t *testing.T) {
	ts := newTestServer(t)
	localText := "DOMAIN-SUFFIX,example.com\nIP-CIDR,1.2.3.0/24"
	resp, err := http.Get(ts.URL + fileURL("rule-set", "surge-rule-set", localText))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "DOMAIN-SUFFIX") {
		t.Errorf("missing DOMAIN-SUFFIX in output:\n%s", bodyStr)
	}
}

func TestRuleParser_UAInference(t *testing.T) {
	// 当 target 为空或 "rule-set" 时，从 User-Agent 推断
	cases := []struct {
		ua   string
		want string
	}{
		{"Surge/5 CFNetwork/1410", "surge"},
		{"Loon/3 iOS", "loon"},
		{"Shadowrocket/1900", "shadowrocket"},
		{"Stash/2", "stash"},
		{"Egern/1", "egern"},
		{"LanceX/1", "lancex"},
		{"UnknownBrowser/1.0", ""}, // 未知 → 不做推断，目标保持空
	}
	for _, c := range cases {
		// 直接调用内部函数更精准
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		got := inferTargetFromUA(req)
		if c.want == "" {
			if got != "" {
				t.Errorf("UA %q: got %q, want empty", c.ua, got)
			}
			continue
		}
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("UA %q: got %q, want prefix %q", c.ua, got, c.want)
		}
	}
}

func TestInferTargetFromUA_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := inferTargetFromUA(req); got != "" {
		t.Errorf("empty UA → got %q, want empty", got)
	}
}

// ── URL 工具函数 ──

func TestBuildScriptHubURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/file/_start_/abc/_end_/?type=qx-rewrite&target=surge", nil)
	got := buildScriptHubURL(req)
	want := "http://script.hub/file/_start_/abc/_end_/?type=qx-rewrite&target=surge"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractReqFromURL(t *testing.T) {
	// 单 URL
	req, arr := extractReqFromURL("http://script.hub/file/_start_/abc/_end_/?x=1")
	if req != "abc" {
		t.Errorf("req = %q, want abc", req)
	}
	if len(arr) != 1 || arr[0] != "abc" {
		t.Errorf("arr = %v, want [abc]", arr)
	}
	// 多 URL（用 %F0%9F%98%82 分隔）
	req2, arr2 := extractReqFromURL("http://script.hub/file/_start_/abc%F0%9F%98%82def/_end_/?x=1")
	if req2 != "abc%F0%9F%98%82def" {
		t.Errorf("req = %q", req2)
	}
	if len(arr2) != 2 || arr2[0] != "abc" || arr2[1] != "def" {
		t.Errorf("arr = %v, want [abc def]", arr2)
	}
	// 无 _start_ / _end_ → 返回空
	req3, arr3 := extractReqFromURL("http://example.com/other")
	if req3 != "" || arr3 != nil {
		t.Errorf("expected empty, got req=%q arr=%v", req3, arr3)
	}
}

func TestExtractURLArg(t *testing.T) {
	got := extractURLArg("http://script.hub/file/_start_/x/_end_/?type=qx-rewrite")
	want := "?type=qx-rewrite"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := extractURLArg("http://example.com/no-end"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDecodeReqArr(t *testing.T) {
	// 单个值
	out := decodeReqArr("hello%20world")
	if len(out) != 1 || out[0] != "hello world" {
		t.Errorf("single: got %v", out)
	}
	// 多个值（%F0%9F%98%82 分隔）
	out2 := decodeReqArr("a%20b%F0%9F%98%82c%20d")
	if len(out2) != 2 || out2[0] != "a b" || out2[1] != "c d" {
		t.Errorf("multi: got %v", out2)
	}
}

func TestBaseURLFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "hub.example.com"
	if got := baseURLFromRequest(req); got != "https://hub.example.com" {
		t.Errorf("default proto: got %q", got)
	}
	req.Header.Set("X-Forwarded-Proto", "http")
	if got := baseURLFromRequest(req); got != "http://hub.example.com" {
		t.Errorf("forwarded proto: got %q", got)
	}
}

// ── script.hub URL 重写 ──

func TestWriteResponse_ReplacesScriptHubURL(t *testing.T) {
	rec := httptest.NewRecorder()
	// 构造一个假的 ResponseWriter，body 中包含 https://script.hub/ 占位
	out := fakeOutput{
		status: 200,
		headers: map[string]string{"Content-Type": "text/plain"},
		body:    "see https://script.hub/file/x for detail",
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "myhost.com"
	writeResponse(rec, out, req)
	body := rec.Body.String()
	if !strings.Contains(body, "https://myhost.com/file/x") {
		t.Errorf("script.hub URL not rewritten:\n%s", body)
	}
	if strings.Contains(body, "script.hub") {
		t.Errorf("script.hub still present:\n%s", body)
	}
}

// fakeOutput 实现 types.ResponseWriter 用于 writeResponse 测试。
type fakeOutput struct {
	status  int
	headers map[string]string
	body    string
}

func (f fakeOutput) GetResponse() types.ResponseData {
	return types.ResponseData{
		Status:  f.status,
		Headers: f.headers,
		Body:    f.body,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
