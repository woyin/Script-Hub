package server

import (
	"bytes"
	"context"
	"errors"
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

func TestRoot_SecurityHeaders(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got == "" {
		t.Error("Referrer-Policy header missing")
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
	// extractReqFromURL 只返回原始（编码后）请求字符串，不再拆分 emoji。
	req := extractReqFromURL("http://script.hub/file/_start_/abc/_end_/?x=1")
	if req != "abc" {
		t.Errorf("req = %q, want abc", req)
	}
	// 多 URL（emoji 分隔符原样保留，拆分由 decodeReqArr 负责）
	req2 := extractReqFromURL("http://script.hub/file/_start_/abc%F0%9F%98%82def/_end_/?x=1")
	if req2 != "abc%F0%9F%98%82def" {
		t.Errorf("req = %q", req2)
	}
	// 无 _start_ / _end_ → 返回空
	req3 := extractReqFromURL("http://example.com/other")
	if req3 != "" {
		t.Errorf("expected empty, got req=%q", req3)
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
	if got := baseURLFromRequest(req); got != "http://hub.example.com" {
		t.Errorf("default proto (no TLS, no forwarded): got %q", got)
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
	if !strings.Contains(body, "myhost.com/file/x") {
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

func TestBaseURLFromRequest_EnvOverride(t *testing.T) {
	old := defaultBaseURLOverride
	defaultBaseURLOverride = "https://cdn.example.com/"
	defer func() { defaultBaseURLOverride = old }()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "internal.local"
	if got := baseURLFromRequest(req); got != "https://cdn.example.com" {
		t.Errorf("BASE_URL override: got %q", got)
	}
}

// ── POST /api/convert ──

func postConvert(t *testing.T, ts *httptest.Server, payload string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/convert", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestConvertAPI_HelpOnGet(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/convert")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestConvertAPI_QXRewriteToSurge(t *testing.T) {
	ts := newTestServer(t)
	payload := `{"type":"qx-rewrite","target":"surge-module","args":{"localtext":"^https?://ads.example.com url reject"}}`
	resp, body := postConvert(t, ts, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "URL Rewrite") && !strings.Contains(body, "reject") {
		t.Errorf("Surge output missing expected content:\n%s", body)
	}
}

func TestConvertAPI_MissingType_400(t *testing.T) {
	ts := newTestServer(t)
	resp, body := postConvert(t, ts, `{"urls":["http://local.text"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "type") {
		t.Errorf("error body should mention type: %s", body)
	}
}

func TestConvertAPI_MissingURLsAndLocaltext_400(t *testing.T) {
	ts := newTestServer(t)
	resp, body := postConvert(t, ts, `{"type":"qx-rewrite","target":"surge-module"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d; body=%s", resp.StatusCode, body)
	}
}

func TestConvertAPI_InvalidJSON_400(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := postConvert(t, ts, `{not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestConvertAPI_UnknownType_400(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := postConvert(t, ts, `{"type":"banana","urls":["http://local.text"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestConvertAPI_BodyTooLarge_413(t *testing.T) {
	ts := newTestServer(t)
	// 构造 > 2MiB 的请求体
	big := strings.Repeat("x", (2<<20)+10)
	payload := `{"type":"qx-rewrite","args":{"localtext":"` + big + `"}}`
	resp, _ := postConvert(t, ts, payload)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	ts := newTestServer(t)
	// 触发一次转换以产生指标
	payload := `{"type":"qx-rewrite","target":"surge-module","args":{"localtext":"^https?://x.com url reject"}}`
	resp, _ := postConvert(t, ts, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prereq convert failed: status %d", resp.StatusCode)
	}

	// 拉 metrics
	mresp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("Get /metrics: %v", err)
	}
	defer mresp.Body.Close()
	body, _ := io.ReadAll(mresp.Body)
	if mresp.StatusCode != http.StatusOK {
		t.Errorf("/metrics status = %d", mresp.StatusCode)
	}
	bs := string(body)
	if !strings.Contains(bs, "scripthub_requests_total") {
		t.Errorf("metrics missing requests_total:\n%s", bs)
	}
	if !strings.Contains(bs, "scripthub_conversions_total") {
		t.Errorf("metrics missing conversions_total:\n%s", bs)
	}
}

func TestFormatsEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/formats")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	bs := string(body)
	for _, want := range []string{"sourceTypes", "rewriteTargets", "ruleTargets", "platforms", "qx-rewrite", "surge-module"} {
		if !strings.Contains(bs, want) {
			t.Errorf("formats missing %q:\n%s", want, bs)
		}
	}
}

// ── statusForError ──

func TestStatusForError_DeadlineExceeded(t *testing.T) {
	got := statusForError(context.DeadlineExceeded)
	if got != http.StatusGatewayTimeout {
		t.Errorf("DeadlineExceeded → %d, want %d", got, http.StatusGatewayTimeout)
	}
}

func TestStatusForError_GenericError(t *testing.T) {
	got := statusForError(errors.New("boom"))
	if got != http.StatusInternalServerError {
		t.Errorf("generic error → %d, want %d", got, http.StatusInternalServerError)
	}
}

// ── writeBody / writeCached ──

func TestWriteBody_ReplacesScriptHubURL(t *testing.T) {
	var buf bytes.Buffer
	hdr := map[string]string{"Content-Type": "text/plain"}
	writeBody(&mockResponseWriter{header: http.Header{}, buf: &buf}, 200, hdr,
		"see https://script.hub/file", "https://my.host")
	got := buf.String()
	if !strings.Contains(got, "https://my.host/file") {
		t.Errorf("script.hub URL not replaced: %q", got)
	}
	if strings.Contains(got, "script.hub") {
		t.Errorf("script.hub placeholder remains: %q", got)
	}
}

func TestWriteCached_ReplacesScriptHubURL(t *testing.T) {
	// cachedResp 现在通过 convertWithCache 返回，走与 writeResponse 相同的 writeBody。
	// URL 替换语义由 writeBody 统一保证（已由 TestWriteBody_* 覆盖）；
	// 这里验证 cachedResp.GetResponse() 产出正确的 ResponseData。
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "cached.example.com"
	cr := cachedResp{
		status:  200,
		headers: map[string]string{"Content-Type": "text/plain"},
		body:    "link: http://script.hub/path",
	}
	rd := cr.GetResponse()
	if rd.Body != cr.body || rd.Status != 200 {
		t.Fatalf("cachedResp.GetResponse = %+v", rd)
	}
	// 走 writeBody 验证替换
	var buf bytes.Buffer
	writeBody(&mockResponseWriter{header: http.Header{}, buf: &buf}, rd.Status, rd.Headers, rd.Body, baseURLFromRequest(r))
	if !strings.Contains(buf.String(), "http://cached.example.com/path") {
		t.Errorf("cached script.hub URL not replaced: %q", buf.String())
	}
	_ = r
}

// ── 缓存命中路径（通过 cacheKey + 实际请求验证）──

func TestCacheKey_SkipsLocaltext(t *testing.T) {
	srv := New(config.LoadConfig())
	ck := srv.cacheKey("qx-rewrite", "surge-module", []string{"http://x"}, map[string]string{"localtext": "abc"})
	if ck != "" {
		t.Errorf("localtext should produce empty cache key, got %q", ck)
	}
}

func TestCacheKey_DisabledWhenNilCache(t *testing.T) {
	srv := New(&config.Config{}) // CacheTTL=0 → cache=nil
	ck := srv.cacheKey("qx-rewrite", "surge-module", []string{"http://x"}, nil)
	if ck != "" {
		t.Errorf("nil cache should produce empty key, got %q", ck)
	}
}

func TestCacheKey_IncludesArgs(t *testing.T) {
	srv := New(&config.Config{CacheTTL: 60})
	ck1 := srv.cacheKey("qx-rewrite", "surge-module", []string{"http://x"}, map[string]string{"policy": "DIRECT"})
	ck2 := srv.cacheKey("qx-rewrite", "surge-module", []string{"http://x"}, map[string]string{"policy": "REJECT"})
	if ck1 == ck2 {
		t.Error("different args should produce different cache keys")
	}
}

// TestConvertWithCache_CorruptedEntryDoesNotDoubleCount 验证缓存条目类型
// 断言失败时不会同时计 cache hit 和 miss（第一轮引入的双重计数 bug）。
// 正常生产中 cache 只写 cachedResp，这里手动注入污染类型模拟异常。
func TestConvertWithCache_CorruptedEntryDoesNotDoubleCount(t *testing.T) {
	srv := New(&config.Config{CacheTTL: 60})
	ck := "qx-rewrite|surge-module|http://x"
	// 注入非 cachedResp 类型的污染条目
	srv.cache.Set(ck, "not-a-cachedResp")

	var buf strings.Builder
	srv.metrics.Render(&buf)
	before := buf.String()
	hitsBefore := strings.Contains(before, "scripthub_cache_hits_total 0")
	missesBefore := strings.Contains(before, "scripthub_cache_misses_total 0")
	if !hitsBefore || !missesBefore {
		t.Fatalf("expected zeroed counters before call, got:\n%s", before)
	}

	// 调用 convertWithCache：类型断言失败 → 应 fall-through 到 miss 路径，
	// 且 parse 返回 error（nil parse 不应被调，但污染会让它走 miss）。
	// 用一个返回 error 的 parse 证明它走了 miss 路径。
	parseCalled := false
	_, err := srv.convertWithCache(ck, "qx-rewrite", "surge-module", func() (types.ResponseWriter, error) {
		parseCalled = true
		return nil, context.DeadlineExceeded
	})
	if !parseCalled {
		t.Error("parse should be called on corrupted cache entry (fall-through to miss)")
	}
	if err == nil {
		t.Error("expected error from parse")
	}

	// 关键断言：hit 不应增加（断言失败不计 hit），miss 应 +1（fall-through）。
	buf.Reset()
	srv.metrics.Render(&buf)
	after := buf.String()
	if strings.Contains(after, "scripthub_cache_hits_total 1") {
		t.Errorf("corrupted entry should NOT count as cache hit:\n%s", after)
	}
	if !strings.Contains(after, "scripthub_cache_misses_total 1") {
		t.Errorf("corrupted entry should fall-through to miss (+1):\n%s", after)
	}
}

// ── runConvert rule-set 分支（API 路径）──

func TestRunConvert_RuleSetFallback(t *testing.T) {
	srv := New(config.LoadConfig())
	ctx := context.Background()
	req := convertRequest{
		Type: "rule-set",
		Args: map[string]string{"localtext": "DOMAIN-SUFFIX,example.com,DIRECT"},
	}
	out, status := srv.runConvert(ctx, req, req.Args)
	if out == nil {
		t.Fatalf("runConvert rule-set failed: status=%d", status)
	}
}

// ── 辅助类型 ──

type mockResponseWriter struct {
	header http.Header
	buf    *bytes.Buffer
	code   int
}

func (m *mockResponseWriter) Header() http.Header {
	if m.header == nil {
		m.header = http.Header{}
	}
	return m.header
}
func (m *mockResponseWriter) WriteHeader(code int) { m.code = code }
func (m *mockResponseWriter) Write(b []byte) (int, error) {
	return m.buf.Write(b)
}
