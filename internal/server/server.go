// Input: context, net/http, github.com/go-chi/chi/v5, internal/config
// Output: type Server, func New(), func (Server) Start(), func (Server) Shutdown()
// Pos: API层-HTTP 服务核心，创建 chi 路由器并管理服务的启动与优雅关闭
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package server 实现 Script Hub 的 HTTP 服务层。
// 使用 chi 路由器，提供与 JS 版 service.js / preview.js 完全对齐的路由逻辑。
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/script-hub-org/script-hub/internal/cache"
	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/metrics"
	"github.com/script-hub-org/script-hub/internal/rewrite"
	"github.com/script-hub-org/script-hub/internal/rule"
)

// Server 是 Script Hub HTTP 服务的核心结构。
type Server struct {
	httpServer *http.Server
	router     *chi.Mux
	cfg        *config.Config
	cache      *cache.Cache // 转换结果缓存；cfg.CacheTTL<=0 时为 nil（禁用）
	metrics    *metrics.Metrics
	flight     *singleflight // 合并相同缓存 key 的并发转换，防缓存击穿
	// 复用的解析器：httpclient.Client 内部的 http.Client 与 headers map 在构造后
	// 只读（SetHeader 未被调用），可被多个 goroutine 并发安全使用。
	// 避免每请求创建新的 http.Client（含 Transport、连接池）。
	rewriteParser *rewrite.Parser
	ruleParser    *rule.Parser
}

// New 创建服务实例。
func New(cfg *config.Config) *Server {
	r := chi.NewRouter()
	rewriteParser := rewrite.NewParser(cfg)
	ruleParser := rule.NewParser(cfg)
	s := &Server{
		router:        r,
		cfg:           cfg,
		cache:         cache.New(cfg.CacheTTL),
		metrics:       metrics.New(),
		flight:        newSingleflight(),
		rewriteParser: rewriteParser,
		ruleParser:    ruleParser,
	}
	// 注入 metrics 到 parser，使其 fetch 失败时能递增计数器。
	rewriteParser.SetMetrics(s.metrics)
	ruleParser.SetMetrics(s.metrics)
	s.setupRoutes()
	return s
}

// Start 在指定地址启动 HTTP 监听。
//
// 超时配置防 slowloris 攻击（慢速发送请求头耗尽连接池）：
//   - ReadHeaderTimeout: 读取请求头的最长时间（10s）。这是 slowloris 的实际攻击
//     向量（慢速 header），单独足以防御。
//   - IdleTimeout:       keep-alive 空闲连接最长存活时间（120s）
//
// 故意不设 ReadTimeout：它是连接级读截止，超时后 server 直接关连接，但不会
// cancel r.Context()，导致正在阻塞的上游 fetch 收到 EOF 而非
// context.DeadlineExceeded —— statusForError 会把合法的超时误判成 500 而非 504。
// 请求总生命周期改由 fileHandler/convertAPIHandler 里的 REQUEST_TIMEOUT context
// 唯一控制。body 阶段的 slowloris 由 convertAPIHandler 的 maxReqBody=2MiB +
// io.LimitReader 封死；GET 端点无 body，不存在慢 body 风险。
func (s *Server) Start(addr string) error {
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown 优雅关闭 HTTP 服务。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
