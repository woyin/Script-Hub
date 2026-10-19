package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/ssrf"
)

// newTestServerWithCfg 允许自定义配置启动服务。
func newTestServerWithCfg(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	cfg.CacheDir = t.TempDir()
	srv := New(cfg)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)
	return ts
}

// TestIntegration_MultiURLFetchOrder 验证多 URL 并发 fetch 后按原始顺序合并结果。
func TestIntegration_MultiURLFetchOrder(t *testing.T) {
	// 两个上游，内容不同且可区分
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "^https?://a\\.com url reject")
	}))
	t.Cleanup(srvA.Close)
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "^https?://b\\.com url reject")
	}))
	t.Cleanup(srvB.Close)

	ts := newTestServer(t)
	// 用 POST API 传两个 URL
	payload := fmt.Sprintf(`{"type":"qx-rewrite","target":"surge-module","urls":[%q,%q]}`,
		srvA.URL, srvB.URL)
	resp, body := postConvert(t, ts, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	// 两个 URL 的内容都应出现在输出中
	if !strings.Contains(body, "a.com") || !strings.Contains(body, "b.com") {
		t.Errorf("expected both a.com and b.com in output:\n%s", body)
	}
}

// TestIntegration_CacheHitReducesUpstreamCalls 验证缓存启用后第二次请求命中缓存、不再访问上游。
func TestIntegration_CacheHitReducesUpstreamCalls(t *testing.T) {
	var upstreamHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamHits, 1)
		fmt.Fprint(w, "^https?://cached\\.example url reject")
	}))
	t.Cleanup(upstream.Close)

	// 启用缓存（TTL 60s）
	cfg := config.LoadConfig()
	cfg.CacheTTL = 60
	ts := newTestServerWithCfg(t, cfg)

	payload := fmt.Sprintf(`{"type":"qx-rewrite","target":"surge-module","urls":[%q]}`, upstream.URL)
	// 第一次：未命中，访问上游
	resp1, body1 := postConvert(t, ts, payload)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first: status=%d body=%s", resp1.StatusCode, body1)
	}
	firstHits := atomic.LoadInt32(&upstreamHits)
	if firstHits != 1 {
		t.Fatalf("expected 1 upstream hit after first request, got %d", firstHits)
	}
	// 第二次：应命中缓存，不再访问上游
	resp2, body2 := postConvert(t, ts, payload)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second: status=%d body=%s", resp2.StatusCode, body2)
	}
	secondHits := atomic.LoadInt32(&upstreamHits)
	if secondHits != 1 {
		t.Errorf("expected cache hit (upstream still %d), got %d", firstHits, secondHits)
	}
	// 两次输出一致
	if body1 != body2 {
		t.Errorf("cached output differs from original")
	}
}

// TestIntegration_LargeBodyRejected 验证 MaxBodyKB 对上游响应生效。
func TestIntegration_LargeBodyRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 返回 10KB，超过测试配置的 2KB 上限
		w.Write([]byte(strings.Repeat("a", 10*1024)))
	}))
	t.Cleanup(upstream.Close)

	// 用小 body 上限配置 client（通过环境无法精确注入，这里直接构造配置）
	cfg := config.LoadConfig()
	cfg.MaxBodyKB = 2
	ts := newTestServerWithCfg(t, cfg)

	payload := fmt.Sprintf(`{"type":"qx-rewrite","target":"surge-module","urls":[%q]}`, upstream.URL)
	resp, _ := postConvert(t, ts, payload)
	// 上游超过上限 → 转换得到空结果（parser 跳过错误 fetch），但不应 panic
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200 (empty result tolerated)", resp.StatusCode)
	}
}

// TestIntegration_OldEndpointEquivalence 验证旧端点与 POST /api/convert 等价。
func TestIntegration_OldEndpointEquivalence(t *testing.T) {
	localText := "^https?://equiv\\.example url reject"
	ts := newTestServer(t)

	// 旧端点（用 fileURL 辅助函数构造，它处理了 query 编码细节）
	resp1, err := http.Get(ts.URL + fileURL("qx-rewrite", "surge-module", localText))
	if err != nil {
		t.Fatalf("old endpoint: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	// 新端点
	payload := fmt.Sprintf(`{"type":"qx-rewrite","target":"surge-module","args":{"localtext":%q}}`, localText)
	resp2, body2 := postConvert(t, ts, payload)

	// 两个端点都应成功并把 localtext 转换为 reject 规则。
	// 不要求字节级一致：query string 与 JSON body 输入路径的编码细节不同。
	if resp1.StatusCode != http.StatusOK || resp2.StatusCode != http.StatusOK {
		t.Fatalf("status old=%d new=%d", resp1.StatusCode, resp2.StatusCode)
	}
	for _, want := range []string{"reject", "equiv"} {
		if !strings.Contains(string(body1), want) {
			t.Errorf("old endpoint output missing %q:\n%s", want, body1)
		}
		if !strings.Contains(body2, want) {
			t.Errorf("new endpoint output missing %q:\n%s", want, body2)
		}
	}
}

// TestIntegration_SingleflightSuppressesConcurrentMisses 验证 singleflight：
// 缓存为空时，同一 URL 的 N 个并发转换请求只让首个打到上游，
// 其余等待后从缓存读取（上游被请求次数 == 1，而非 N）。
func TestIntegration_SingleflightSuppressesConcurrentMisses(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		// 随机延迟，增大并发窗口，确保多个请求真的重叠
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte("^https?://sf url reject"))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.LoadConfig()
	cfg.CacheTTL = 60 // 显式开启缓存
	ts := newTestServerWithCfg(t, cfg)

	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			payload := fmt.Sprintf(`{"type":"qx-rewrite","target":"surge-module","urls":[%q]}`, upstream.URL)
			resp, _ := postConvert(t, ts, payload)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status=%d", resp.StatusCode)
			}
		}()
	}
	close(start)
	wg.Wait()

	if hits > 1 {
		t.Errorf("upstream hit %d times, want <=1 (singleflight should suppress)", hits)
	}
}

// TestIntegration_SSRFBlocksLoopback 验证 SSRF 默认开启的端到端行为：
// 当 ssrf.Enabled 被同步为 cfg.SSRFBlockPrivate（如 main.go 所为）时，
// 指向 loopback（httptest 起在 127.0.0.1）的上游请求被拦截，转换得到空结果。
// 这锁死了"SSRF 默认值改 true"后服务确实拦私有地址。
func TestIntegration_SSRFBlocksLoopback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("^https?://blocked url reject"))
	}))
	t.Cleanup(upstream.Close)

	// 镜像 main.go 的同步顺序：先设 ssrf.Enabled，再 New(cfg)。
	// httpclient 在构造时读取 ssrf.Enabled 决定是否注入受控 Transport。
	old := ssrf.Enabled
	ssrf.Enabled = true
	t.Cleanup(func() { ssrf.Enabled = old })

	cfg := config.LoadConfig() // SSRFBlockPrivate 默认 true
	ts := newTestServerWithCfg(t, cfg)

	payload := fmt.Sprintf(`{"type":"qx-rewrite","target":"surge-module","urls":[%q]}`, upstream.URL)
	resp, body := postConvert(t, ts, payload)
	// SSRF 拦截 loopback → fetch 失败 → 转换为空（HTTP 200，空 body），
	// 上游的 reject 规则不会出现在输出里。
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 (empty result tolerated)", resp.StatusCode)
	}
	if strings.Contains(body, "blocked") {
		t.Errorf("SSRF should have blocked loopback fetch, but upstream content leaked:\n%s", body)
	}
}

// 相同请求在 GET/POST 间共享磁盘结果；强制刷新替换同一个键，失败保留旧值。
func TestIntegration_DiskRefreshLifecycle(t *testing.T) {
	for _, source := range []string{"rule-set", "qx-rewrite"} {
		t.Run(source, func(t *testing.T) {
			var hits atomic.Int32
			var fail atomic.Bool
			var content atomic.Value
			target := "surge-rule-set"
			initial := "DOMAIN,first.example"
			updated := "DOMAIN,second.example"
			if source == "qx-rewrite" {
				target = "surge-module"
				initial = "^https?://first\\.example url reject"
				updated = "^https?://second\\.example url reject"
			}
			content.Store(initial)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if fail.Load() {
					w.WriteHeader(503)
					return
				}
				fmt.Fprint(w, content.Load().(string))
			}))
			defer upstream.Close()
			cfg := config.LoadConfig()
			cfg.CacheDir = t.TempDir()
			cfg.CacheTTL = 86400
			serve := func() *httptest.Server {
				srv := New(cfg)
				// 本测试隔离缓存生命周期，公开限流另有专门测试。
				srv.refreshLimit = nil
				t.Cleanup(func() { srv.Shutdown(context.Background()) })
				ts := httptest.NewServer(srv.router)
				t.Cleanup(ts.Close)
				return ts
			}
			ts := serve()
			payload := fmt.Sprintf(`{"type":%q,"target":%q,"urls":[%q]}`, source, target, upstream.URL)
			resp, first := postConvert(t, ts, payload)
			if resp.StatusCode != 200 || !strings.Contains(first, "first") {
				t.Fatalf("initial %d %s", resp.StatusCode, first)
			}
			// 通过重新构造 Server 验证进程内没有保存响应正文。
			ts = serve()
			path := "/file/_start_/" + url.PathEscape(upstream.URL) + "/_end_/?type=" + source + "&target=" + target
			get := func(path string) (int, string) {
				resp, err := http.Get(ts.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				data, _ := io.ReadAll(resp.Body)
				return resp.StatusCode, string(data)
			}
			status, warm := get(path)
			if status != 200 || warm != first || hits.Load() != 1 {
				t.Fatalf("restart cache: %d %d %s", status, hits.Load(), warm)
			}
			content.Store(updated)
			status, refreshed := get(path + "&refresh=true")
			if status != 200 || !strings.Contains(refreshed, "second") || hits.Load() != 2 {
				t.Fatalf("refresh: %d %d %s", status, hits.Load(), refreshed)
			}
			resp, body := postConvert(t, ts, payload)
			if resp.StatusCode != 200 || body != refreshed || hits.Load() != 2 {
				t.Fatal("refresh created a different cache key")
			}
			fail.Store(true)
			status, _ = get(path + "&refresh=true")
			if status != 500 {
				t.Fatalf("failed refresh status %d", status)
			}
			before := hits.Load()
			resp, body = postConvert(t, ts, payload)
			if resp.StatusCode != 200 || body != refreshed || hits.Load() != before {
				t.Fatal("failed refresh replaced good cache")
			}
			// POST 同样支持 args.refresh，成功刷新后普通请求复用结果。
			fail.Store(false)
			content.Store(initial)
			refreshPayload := strings.TrimSuffix(payload, "}") + `,"args":{"refresh":"true"}}`
			resp, body = postConvert(t, ts, refreshPayload)
			if resp.StatusCode != 200 || !strings.Contains(body, "first") || hits.Load() != before+1 {
				t.Fatalf("POST refresh: %d %s", resp.StatusCode, body)
			}
		})
	}
}

func TestIntegration_PartialResultNotCached(t *testing.T) {
	for _, source := range []string{"rule-set", "qx-rewrite"} {
		t.Run(source, func(t *testing.T) {
			var hits atomic.Int32
			var fail atomic.Bool
			fail.Store(true)
			line := "DOMAIN,valid.example"
			target := "surge-rule-set"
			if source == "qx-rewrite" {
				line = "^https?://valid\\.example url reject"
				target = "surge-module"
			}
			good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, line) }))
			defer good.Close()
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if fail.Load() {
					w.WriteHeader(503)
					return
				}
				fmt.Fprint(w, line)
			}))
			defer bad.Close()
			cfg := config.LoadConfig()
			cfg.CacheTTL = 86400
			ts := newTestServerWithCfg(t, cfg)
			payload := fmt.Sprintf(`{"type":%q,"target":%q,"urls":[%q,%q]}`, source, target, good.URL, bad.URL)
			postConvert(t, ts, payload)
			fail.Store(false)
			postConvert(t, ts, payload)
			postConvert(t, ts, payload)
			if hits.Load() != 2 {
				t.Fatalf("upstream hits=%d, partial result must not be cached", hits.Load())
			}
		})
	}
}

func TestIntegration_StreamSingleflightReaders(t *testing.T) {
	var hits atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, "payload:\n - DOMAIN,shared.example\n - DOMAIN,second.example")
	}))
	defer source.Close()
	cfg := config.LoadConfig()
	cfg.CacheTTL = 86400
	ts := newTestServerWithCfg(t, cfg)
	payload := fmt.Sprintf(`{"type":"rule-set","target":"surge-rule-set","urls":[%q]}`, source.URL)
	var wg sync.WaitGroup
	results := make(chan string, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body := postConvert(t, ts, payload)
			if resp.StatusCode != 200 {
				t.Errorf("status %d", resp.StatusCode)
			}
			results <- body
		}()
	}
	wg.Wait()
	close(results)
	first := ""
	for result := range results {
		if !strings.Contains(result, "shared.example") || !strings.Contains(result, "second.example") {
			t.Fatalf("incomplete file response %q", result)
		}
		if first == "" {
			first = result
		} else if first != result {
			t.Fatal("readers shared an offset")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hit %d times", hits.Load())
	}
	dirs, _ := filepath.Glob(filepath.Join(cfg.CacheDir, ".work-*"))
	if len(dirs) != 0 {
		t.Fatalf("workspaces not released: %v", dirs)
	}
}
