// Input: fmt, log, net/http, net/url, strings, internal/frontend, internal/rewrite, internal/rule, internal/types, internal/util
// Output: func (Server) scriptHubHandler/rewriteParserHandler/ruleParserHandler(), func writeResponse(), func decodeReqArr(), func inferTargetFromUA(), func baseURLFromRequest()
// Pos: API层-请求处理器，调用各解析器并统一写回 HTTP 响应
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// handler.go 实现 Script Hub HTTP 请求处理器。
// 每个处理器对应 JS 版 scriptMap.js 中的一条路由规则：
//   - scriptHubHandler    → script-hub.js（前端 UI）
//   - rewriteParserHandler → Rewrite-Parser.js（重写转换）
//   - ruleParserHandler   → rule-parser.js（规则集转换）
package server

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/script-hub-org/script-hub/internal/frontend"
	"github.com/script-hub-org/script-hub/internal/rewrite"
	"github.com/script-hub-org/script-hub/internal/rule"
	"github.com/script-hub-org/script-hub/internal/types"
	"github.com/script-hub-org/script-hub/internal/util"
)

// scriptHubHandler 处理转换页面请求。
// 对应 JS 版 scriptMap.js 中 script-hub.js 的路由规则。
func (s *Server) scriptHubHandler(w http.ResponseWriter, r *http.Request) {

	baseURL := baseURLFromRequest(r)
	html := frontend.GenerateHTML(baseURL)
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(html))
}

// rewriteParserHandler 处理重写转换请求（QX重写/Surge模块/Loon插件/全模块）。
// 对应 JS 版 scriptMap.js 中 Rewrite-Parser.js 的路由规则。
// URL 格式: /file/_start_/{encoded_url}/_end_/?type={source_type}&target={target_app}
func (s *Server) rewriteParserHandler(w http.ResponseWriter, r *http.Request) {
	scriptURL := buildScriptHubURL(r)

	req, _ := extractReqFromURL(scriptURL)
	urlArg := extractURLArg(scriptURL)
	queryParams := util.ParseQueryString(urlArg)

	parser := rewrite.NewParser(s.cfg)
	input := rewrite.ParseInput{
		URLs:       decodeReqArr(req),
		SourceType: queryParams["type"],
		TargetApp:  queryParams["target"],
		Arguments:  queryParams,
	}

	output, err := parser.Parse(r.Context(), input)
	if err != nil {
		log.Printf("rewriteParser error: %v", err)
		http.Error(w, fmt.Sprintf("Rewrite parse error: %v", err), http.StatusInternalServerError)
		return
	}

	writeResponse(w, output, r)
}

// ruleParserHandler 处理规则集转换请求。
// 对应 JS 版 scriptMap.js 中 rule-parser.js 的路由规则。
// 当 target 为通用 "rule-set" 时，自动从 User-Agent 推断目标平台。
func (s *Server) ruleParserHandler(w http.ResponseWriter, r *http.Request) {
	scriptURL := buildScriptHubURL(r)

	req, _ := extractReqFromURL(scriptURL)
	urlArg := extractURLArg(scriptURL)
	queryParams := util.ParseQueryString(urlArg)

	parser := rule.NewParser(s.cfg)
	target := queryParams["target"]
	if target == "" || target == "rule-set" {
		// 镜像 JS 行为：当 target 为通用的 "rule-set" 时，
		// 从请求 User-Agent 推断目标平台
		if inferred := inferTargetFromUA(r); inferred != "" {
			target = inferred
		}
	}
	input := rule.ParseInput{
		URLs:      decodeReqArr(req),
		TargetApp: target,
		Arguments: queryParams,
	}

	output, err := parser.Parse(r.Context(), input)
	if err != nil {
		log.Printf("ruleParser error: %v", err)
		http.Error(w, fmt.Sprintf("Rule parse error: %v", err), http.StatusInternalServerError)
		return
	}

	writeResponse(w, output, r)
}

// writeResponse 将解析器输出写入 HTTP 响应。
// 统一处理：设置响应头、将 script.hub URL 替换为实际服务地址。
// 对应 JS 版 service.js 中的 ctx.body 替换逻辑。
func writeResponse(w http.ResponseWriter, output types.ResponseWriter, r *http.Request) {
	resp := output.GetResponse()
	baseURL := baseURLFromRequest(r)

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	body := resp.Body
	body = strings.ReplaceAll(body, "https://script.hub/", baseURL+"/")
	body = strings.ReplaceAll(body, "http://script.hub/", baseURL+"/")
	w.WriteHeader(resp.Status)
	w.Write([]byte(body))
}

// decodeReqArr 解码请求 URL 数组。
// 对应 JS 版 Rewrite-Parser.js 中的 reqArr 解析逻辑：
// 多个 URL 用 😂（%F0%9F%98%82）表情符号分隔。
func decodeReqArr(req string) []string {
	if strings.Contains(req, "%F0%9F%98%82") {
		parts := strings.Split(req, "%F0%9F%98%82")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			decoded, err := url.QueryUnescape(p)
			if err != nil {
				decoded = p
			}
			result = append(result, decoded)
		}
		return result
	}
	decoded, err := url.QueryUnescape(req)
	if err != nil {
		decoded = req
	}
	return []string{decoded}
}

// inferTargetFromUA 根据请求 User-Agent 推断规则集目标平台。
// 对应 JS 版 rule-parser.js 中的 UA 检测逻辑。
// 支持识别：Surge/LanceX/Egern、Stash、Loon、Shadowrocket。
func inferTargetFromUA(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		return ""
	}
	switch {
	case strings.Contains(ua, "Egern"):
		return "egern-rule-set"
	case strings.Contains(ua, "LanceX"):
		return "lancex-rule-set"
	case strings.Contains(ua, "Surge"):
		return "surge-rule-set"
	case strings.Contains(ua, "Stash"):
		return "stash-rule-set"
	case strings.Contains(ua, "Loon"):
		return "loon-rule-set"
	case strings.Contains(ua, "Shadowrocket"):
		return "shadowrocket-rule-set"
	}
	return ""
}

// baseURLFromRequest 从请求的 Host header 自动推导服务地址。
// 优先使用 X-Forwarded-Proto（反向代理场景），否则默认 https。
func baseURLFromRequest(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "https"
	}
	return proto + "://" + r.Host
}
