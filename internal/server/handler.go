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
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/frontend"
	"github.com/script-hub-org/script-hub/internal/rewrite"
	"github.com/script-hub-org/script-hub/internal/rule"
	"github.com/script-hub-org/script-hub/internal/types"
	"github.com/script-hub-org/script-hub/internal/util"
)

// defaultBaseURLOverride 在进程启动时读取一次 BASE_URL 环境变量，
// 用于强制覆盖服务对外暴露的地址（如部署在多层代理后）。
// 空字符串表示未设置，回退到按请求推断。
var defaultBaseURLOverride = os.Getenv("BASE_URL")

// scriptHubHandler 处理转换页面请求。
// 对应 JS 版 scriptMap.js 中 script-hub.js 的路由规则。
func (s *Server) scriptHubHandler(w http.ResponseWriter, r *http.Request) {

	baseURL := baseURLFromRequest(r)
	html := frontend.GenerateHTML(baseURL)
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// 安全响应头：防止 Web UI 被 iframe 嵌套（点击劫持），
	// 限制内联脚本只信任自身源。前端 HTML 内联 CSS/JS 无外部依赖，
	// 故 CSP 仅允许 'unsafe-inline'（页面本身是可信的静态模板）。
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Write([]byte(html))
}

// rewriteParserHandler 处理重写转换请求（QX重写/Surge模块/Loon插件/全模块）。
// 对应 JS 版 scriptMap.js 中 Rewrite-Parser.js 的路由规则。
// URL 格式: /file/_start_/{encoded_url}/_end_/?type={source_type}&target={target_app}
func (s *Server) rewriteParserHandler(w http.ResponseWriter, r *http.Request) {
	s.metrics.IncRequest()
	scriptURL := buildScriptHubURL(r)

	req := extractReqFromURL(scriptURL)
	urlArg := extractURLArg(scriptURL)
	queryParams := util.ParseQueryString(urlArg)

	input := rewrite.ParseInput{
		URLs:       decodeReqArr(req),
		SourceType: queryParams["type"],
		TargetApp:  queryParams["target"],
		Arguments:  queryParams,
	}

	// 缓存命中检查（仅对不含 localtext 的远程转换生效）
	ck := s.cacheKey(input.SourceType, input.TargetApp, input.URLs, input.Arguments)
	output, err := s.convertWithCache(ck, input.SourceType, input.TargetApp, func() (types.ResponseWriter, error) {
		return s.rewriteParser.Parse(r.Context(), input)
	})
	if err != nil {
		log.Printf("rewriteParser error: %v", err)
		s.metrics.IncConversionError()
		http.Error(w, "Rewrite parse error", statusForError(err))
		return
	}
	writeResponse(w, output, r)
}

// ruleParserHandler 处理规则集转换请求。
// 对应 JS 版 scriptMap.js 中 rule-parser.js 的路由规则。
// 当 target 为通用 "rule-set" 时，自动从 User-Agent 推断目标平台。
func (s *Server) ruleParserHandler(w http.ResponseWriter, r *http.Request) {
	s.metrics.IncRequest()
	scriptURL := buildScriptHubURL(r)

	req := extractReqFromURL(scriptURL)
	urlArg := extractURLArg(scriptURL)
	queryParams := util.ParseQueryString(urlArg)

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

	ck := s.cacheKey("rule-set", input.TargetApp, input.URLs, input.Arguments)
	output, err := s.convertWithCache(ck, "rule-set", input.TargetApp, func() (types.ResponseWriter, error) {
		return s.ruleParser.Parse(r.Context(), input)
	})
	if err != nil {
		log.Printf("ruleParser error: %v", err)
		s.metrics.IncConversionError()
		http.Error(w, "Rule parse error", statusForError(err))
		return
	}
	writeResponse(w, output, r)
}

// writeBody 将 status/headers/body 写入响应，并将 script.hub 占位符替换为实际 baseURL。
func writeBody(w http.ResponseWriter, status int, headers map[string]string, body, baseURL string) {
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	body = strings.ReplaceAll(body, "https://script.hub/", baseURL+"/")
	body = strings.ReplaceAll(body, "http://script.hub/", baseURL+"/")
	w.WriteHeader(status)
	w.Write([]byte(body))
}

// writeResponse 将解析器输出写入 HTTP 响应。
// 统一处理：设置响应头、将 script.hub URL 替换为实际服务地址。
// 对应 JS 版 service.js 中的 ctx.body 替换逻辑。
func writeResponse(w http.ResponseWriter, output types.ResponseWriter, r *http.Request) {
	resp := output.GetResponse()
	writeBody(w, resp.Status, resp.Headers, resp.Body, baseURLFromRequest(r))
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

// baseURLFromRequest 从请求推导服务对外地址。
// 推断优先级：BASE_URL 环境变量 > X-Forwarded-Proto 头 > r.TLS > 默认 http。
// 本地直连（无 TLS、无代理头）时返回 http，避免注入成 https 导致链接不可用。
func baseURLFromRequest(r *http.Request) string {
	if defaultBaseURLOverride != "" {
		return strings.TrimRight(defaultBaseURLOverride, "/")
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + r.Host
}

// convertRequest 是 POST /api/convert 的 JSON 请求体。
type convertRequest struct {
	URLs   []string          `json:"urls,omitempty"`
	Type   string            `json:"type"`
	Target string            `json:"target,omitempty"`
	Args   map[string]string `json:"args,omitempty"`
}

// convertErrorResponse 是 API 错误响应的 JSON 结构。
type convertErrorResponse struct {
	Error string `json:"error"`
}

// cachedResp 是缓存中保存的一次转换的完整响应数据。
type cachedResp struct {
	status  int
	headers map[string]string
	body    string
}

// GetResponse 让 cachedResp 实现 types.ResponseWriter，便于缓存命中时直接返回。
func (c cachedResp) GetResponse() types.ResponseData {
	return types.ResponseData{Status: c.status, Headers: c.headers, Body: c.body}
}

// convertAPIHelpHandler 在 GET /api/convert 返回简易用法说明（纯文本）。
func (s *Server) convertAPIHelpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	const help = `POST /api/convert

Request body (JSON):
  {
    "urls":   ["https://example.com/module.sgmodule"],  // 远程地址；为空时必须提供 args.localtext
    "type":   "qx-rewrite|surge-module|loon-plugin|all-module|rule-set",
    "target": "surge-module|loon-plugin|...",           // 可选
    "args":   { "localtext": "...", "policy": "DIRECT" } // 可选，等价于旧端点的查询参数
  }

Response: 转换后的原始文本，Content-Type 由目标格式决定。
`
	w.Write([]byte(help))
}

// convertAPIHandler 处理 POST /api/convert，是语义化的转换 API。
// 与旧 /file/_start_/.../_end_/ 端点功能等价，但使用结构化 JSON 请求体，
// 避免在 URL 路径里嵌套 URL 与 emoji 分隔符，且支持通过 POST body 传入大段 localtext。
// 旧端点保留不变，向后兼容。
func (s *Server) convertAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 限制请求体大小，避免恶意大 JSON。
	const maxReqBody = 2 << 20 // 2 MiB
	body, err := io.ReadAll(io.LimitReader(r.Body, maxReqBody+1))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "cannot read request body")
		return
	}
	if int64(len(body)) > maxReqBody {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "request body exceeds 2MiB")
		return
	}

	var req convertRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Type == "" {
		writeAPIError(w, http.StatusBadRequest, "missing required field: type")
		return
	}

	// 合并 args 到 Arguments；显式字段优先。
	args := make(map[string]string, len(req.Args)+2)
	for k, v := range req.Args {
		args[k] = v
	}
	if _, ok := args["localtext"]; !ok && len(req.URLs) == 0 {
		writeAPIError(w, http.StatusBadRequest, "either urls or args.localtext must be provided")
		return
	}

	// 端到端超时，与 fileHandler 行为一致。
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.RequestTimeout)*time.Second)
	defer cancel()

	output, status := s.runConvert(ctx, req, args)
	if output == nil {
		writeAPIError(w, status, "conversion failed")
		return
	}

	resp := output.GetResponse()
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(resp.Status)
	w.Write([]byte(resp.Body))
}

// runConvert 按 sourceType 选择解析器并执行转换。
// 返回 (输出, 错误HTTP状态码)；输出非 nil 表示成功。
func (s *Server) runConvert(ctx context.Context, req convertRequest, args map[string]string) (types.ResponseWriter, int) {
	s.metrics.IncRequest()
	switch req.Type {
	case config.SourceTypeQXRewrite,
		config.SourceTypeSurgeModule,
		config.SourceTypeLoonPlugin,
		config.SourceTypeAllModule:
		ck := s.cacheKey(req.Type, req.Target, req.URLs, args)
		input := rewrite.ParseInput{
			URLs:       req.URLs,
			SourceType: req.Type,
			TargetApp:  req.Target,
			Arguments:  args,
		}
		out, err := s.convertWithCache(ck, req.Type, req.Target, func() (types.ResponseWriter, error) {
			return s.rewriteParser.Parse(ctx, input)
		})
		if err != nil {
			log.Printf("api rewriteParser error: %v", err)
			s.metrics.IncConversionError()
			return nil, statusForError(err)
		}
		return out, 0
	case config.SourceTypeRuleSet:
		ck := s.cacheKey("rule-set", req.Target, req.URLs, args)
		target := req.Target
		if target == "" || target == "rule-set" {
			// API 无 UA 可推断，target 为空时退化为 surge-rule-set。
			target = "surge-rule-set"
		}
		input := rule.ParseInput{
			URLs:      req.URLs,
			TargetApp: target,
			Arguments: args,
		}
		out, err := s.convertWithCache(ck, "rule-set", req.Target, func() (types.ResponseWriter, error) {
			return s.ruleParser.Parse(ctx, input)
		})
		if err != nil {
			log.Printf("api ruleParser error: %v", err)
			s.metrics.IncConversionError()
			return nil, statusForError(err)
		}
		return out, 0
	default:
		return nil, http.StatusBadRequest
	}
}

// writeAPIError 写入统一的 JSON 错误响应。
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(convertErrorResponse{Error: msg})
}

// convertWithCache 统一处理"查缓存 → 未命中则执行 parse（受 singleflight 保护）→ 写缓存"。
// source/target 用于递增按维度的转换计数；命中和未命中都计数。
// ck 为空串（含 localtext 或缓存禁用）时直接执行 parse，不走缓存与 singleflight。
func (s *Server) convertWithCache(ck, source, target string, parse func() (types.ResponseWriter, error)) (types.ResponseWriter, error) {
	// 不可缓存：直接执行
	if ck == "" {
		return parse()
	}
	// 缓存命中：先做类型断言，仅当命中且类型正确才计 hit 并返回。
	// 断言失败（理论上不会发生——cache 只写 cachedResp 一种类型）时
	// fall-through 到 miss 路径，避免同时计 hit+miss 导致计数错乱。
	if cached, ok := s.cache.Get(ck); ok {
		if cr, ok := cached.(cachedResp); ok {
			s.metrics.IncCacheHit()
			s.metrics.IncConversion(source, target)
			return cr, nil
		}
	}
	s.metrics.IncCacheMiss()
	// singleflight 合并同 key 并发，防缓存击穿：首个请求执行 parse，
	// 其余复用其结果（成功后首个已写入缓存，其余随后从缓存读）。
	out, err := s.flight.do(ck, func() (any, error) {
		o, e := parse()
		if e == nil && o != nil {
			rd := o.GetResponse()
			s.cache.Set(ck, cachedResp{status: rd.Status, headers: rd.Headers, body: rd.Body})
		}
		return o, e
	})
	if err != nil {
		return nil, err
	}
	s.metrics.IncConversion(source, target)
	return out.(types.ResponseWriter), nil
}

// statusForError 将解析器返回的错误映射为合适的 HTTP 状态码。
// 超时（DeadlineExceeded）返回 504，其余仍返回 500。
func statusForError(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

// cacheKey 为一次转换构造缓存键。
// args 中的 localtext 使缓存失效（私有正文不缓存）。
// 返回空串表示不应缓存（如含 localtext，或 cache 被禁用）。
func (s *Server) cacheKey(sourceType, target string, urls []string, args map[string]string) string {
	if s.cache == nil {
		return ""
	}
	if args["localtext"] != "" {
		return ""
	}
	// 仅缓存纯远程转换
	if len(urls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(sourceType)
	b.WriteByte('|')
	b.WriteString(target)
	b.WriteByte('|')
	for i, u := range urls {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(u)
	}
	// args 中除 localtext 外的参数会影响输出，需纳入 key。
	// 按固定顺序拼接以保证可重复。
	keys := make([]string, 0, len(args))
	for k := range args {
		if k != "localtext" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(args[k])
	}
	return b.String()
}

// metricsHandler 输出 Prometheus 文本格式的运行时指标。
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.Render(w)
}

// formatsDocument 描述实例支持的格式能力，启动时静态确定。
type formatsDocument struct {
	SourceTypes    []string `json:"sourceTypes"`
	RewriteTargets []string `json:"rewriteTargets"`
	RuleTargets    []string `json:"ruleTargets"`
	Platforms      []string `json:"platforms"`
}

// supportedFormats 是 /formats 端点返回的能力清单。
// 内容来自 internal/config 的常量，保证与实际分发逻辑一致。
var supportedFormats = formatsDocument{
	SourceTypes: []string{
		config.SourceTypeQXRewrite,
		config.SourceTypeSurgeModule,
		config.SourceTypeLoonPlugin,
		config.SourceTypeAllModule,
		config.SourceTypeRuleSet,
	},
	RewriteTargets: []string{
		config.TargetSurgeModule,
		config.TargetStashStoverride,
		config.TargetLoonPlugin,
		config.TargetShadowrocketModule,
		config.TargetEgernModule,
		config.TargetLanceXModule,
		config.TargetQXRewrite,
	},
	RuleTargets: []string{
		config.TargetSurgeRuleSet,
		config.TargetStashRuleSet,
		config.TargetLoonRuleSet,
		config.TargetShadowrocketRuleSet,
		config.TargetEgernRuleSet,
		config.TargetLanceXRuleSet,
		config.TargetSurgeDomainSet,
		config.TargetSurgeDomainSet2,
		config.TargetStashDomainSet,
		config.TargetStashDomainSet2,
	},
	Platforms: []string{
		string(config.PlatformQX),
		string(config.PlatformSurge),
		string(config.PlatformLoon),
		string(config.PlatformStash),
		string(config.PlatformShadowrocket),
		string(config.PlatformEgern),
		string(config.PlatformLanceX),
	},
}

// formatsHandler 返回当前实例支持的格式能力（JSON），便于客户端运行时探测。
func (s *Server) formatsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(supportedFormats)
}
