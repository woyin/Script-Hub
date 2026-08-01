// Input: net/http, strings, internal/config
// Output: func (Server) setupRoutes(), func (Server) dispatchHandler(), func (Server) fileHandler(), func buildScriptHubURL(), func extractReqFromURL(), func extractURLArg()
// Pos: API层-路由分发，全捕获后按 URL 手动分发到重写/规则解析器
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// router.go 实现 Script Hub 的 URL 路由分发。
// 使用全捕获处理器配合手动 URL 分发，因为 URL 中可能包含 "://" 字符，
// chi 的模式匹配器无法正确处理。
// 路由逻辑与 JS 版 scriptMap.js 中的正则路由一一对应。
package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/util"
)

// setupRoutes 设置全捕获路由，将所有请求转发到 dispatchHandler。
func (s *Server) setupRoutes() {
	// 语义化 API 端点优先注册，chi 按注册顺序精确匹配优先于 /* 全捕获。
	s.router.Post("/api/convert", s.convertAPIHandler)
	s.router.Get("/api/convert", s.convertAPIHelpHandler)
	s.router.Get("/formats", s.formatsHandler)
	s.router.Get("/metrics", s.metricsHandler)
	s.router.Get("/*", s.dispatchHandler)
}

// dispatchHandler 实现与 JS 版 scriptMap.js 相同的路由逻辑：
//   - /                  → 转换页面
//   - /healthz           → 健康检查
//   - /version           → 返回应用版本号（纯文本）
//   - /file/_start_/...   → 重写/规则解析器
func (s *Server) dispatchHandler(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.RequestURI()

	switch {
	case uri == "/healthz":
		w.WriteHeader(http.StatusOK)

	case uri == "/version":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(s.cfg.Version))

	case uri == "/":
		s.scriptHubHandler(w, r)

	case strings.Contains(uri, "/file/_start_/"):
		s.fileHandler(w, r)

	default:
		http.NotFound(w, r)
	}
}

// fileHandler 根据 "type" 查询参数分发到重写解析器或规则解析器。
// 对应 JS 版 scriptMap.js 中的类型匹配：
//   - qx-rewrite / surge-module / loon-plugin / all-module → Rewrite-Parser
//   - rule-set → rule-parser
func (s *Server) fileHandler(w http.ResponseWriter, r *http.Request) {
	// 为整个转换请求设置端到端超时，覆盖内部所有上游 fetch 与解析。
	// 避免多 URL 串行 fetch 时单请求总耗时 = N × HTTP_TIMEOUT。
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.RequestTimeout)*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	// 与 handler 内部使用同一条解析路径（extractURLArg + util.ParseQueryString），
	// 避免 router 与 handler 对同一请求得到不同的 type。
	scriptURL := buildScriptHubURL(r)
	urlArg := extractURLArg(scriptURL)
	queryParams := util.ParseQueryString(urlArg)
	queryType := queryParams["type"]
	switch {
	case queryType == config.SourceTypeQXRewrite,
		queryType == config.SourceTypeSurgeModule,
		queryType == config.SourceTypeLoonPlugin,
		queryType == config.SourceTypeAllModule:
		s.rewriteParserHandler(w, r)
	case queryType == config.SourceTypeRuleSet:
		s.ruleParserHandler(w, r)
	default:
		http.Error(w, "Unknown type parameter", http.StatusBadRequest)
	}
}

// ── URL 解析工具函数 ──
// 以下函数实现了与 JS 版 Rewrite-Parser.js 相同的 URL 解析逻辑。

// buildScriptHubURL 将 HTTP 请求的 URL 重构为 "http://script.hub" 格式，
// 以便与 JS 版的路由匹配逻辑一致。
func buildScriptHubURL(r *http.Request) string {
	return "http://script.hub" + r.URL.RequestURI()
}

// extractReqFromURL 从 URL 中提取 /file/_start_/ 和 /_end_/ 之间的编码路径。
// 仅返回原始（编码后的）请求字符串；emoji 分隔符的拆分与解码统一由 decodeReqArr 负责。
func extractReqFromURL(rawURL string) string {
	parts := strings.SplitN(rawURL, "/file/_start_/", 2)
	if len(parts) < 2 {
		return ""
	}
	rest := parts[1]
	endParts := strings.SplitN(rest, "/_end_/", 2)
	if len(endParts) < 1 {
		return ""
	}
	return endParts[0]
}

// extractURLArg 提取 /_end_/ 之后的 URL 部分（查询参数区域）。
func extractURLArg(rawURL string) string {
	parts := strings.SplitN(rawURL, "/_end_/", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
