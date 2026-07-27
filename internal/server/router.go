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
	"net/http"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
)

// setupRoutes 设置全捕获路由，将所有请求转发到 dispatchHandler。
func (s *Server) setupRoutes() {
	s.router.Get("/*", s.dispatchHandler)
}

// dispatchHandler 实现与 JS 版 scriptMap.js 相同的路由逻辑：
//   - /                  → 转换页面
//   - /healthz           → 健康检查
//   - /file/_start_/...   → 重写/规则解析器
func (s *Server) dispatchHandler(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.RequestURI()

	switch {
	case uri == "/healthz":
		w.WriteHeader(http.StatusOK)

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
	queryType := r.URL.Query().Get("type")
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
// 返回编码后的请求路径和已分割的数组（当 URL 包含 😂 分隔符时）。
func extractReqFromURL(rawURL string) (string, []string) {
	parts := strings.SplitN(rawURL, "/file/_start_/", 2)
	if len(parts) < 2 {
		return "", nil
	}
	rest := parts[1]
	endParts := strings.SplitN(rest, "/_end_/", 2)
	if len(endParts) < 1 {
		return "", nil
	}
	req := endParts[0]

	// 😂 表情（%F0%9F%98%82）用作多 URL 分隔符
	if strings.Contains(req, "%F0%9F%98%82") {
		return req, strings.Split(req, "%F0%9F%98%82")
	}
	return req, []string{req}
}

// extractURLArg 提取 /_end_/ 之后的 URL 部分（查询参数区域）。
func extractURLArg(rawURL string) string {
	parts := strings.SplitN(rawURL, "/_end_/", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
