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
	"strings"
	"time"

	"github.com/script-hub-org/script-hub/internal/cache"
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
	if queryParams["refresh"] == "true" && !s.allowPublicRefresh(w, ck) {
		return
	}
	output, err := s.convertWithCacheContext(r.Context(), ck, input.SourceType, input.TargetApp, func() (types.ResponseWriter, error) {
		return s.rewriteParser.Parse(r.Context(), input)
	}, queryParams["refresh"] == "true")
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
	if queryParams["refresh"] == "true" && !s.allowPublicRefresh(w, ck) {
		return
	}
	output, err := s.convertWithCacheContext(r.Context(), ck, "rule-set", input.TargetApp, func() (types.ResponseWriter, error) {
		return s.parseRules(r.Context(), input, ck)
	}, queryParams["refresh"] == "true")
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
	sendResponse(w, resp, baseURLFromRequest(r), true)
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
type convertRequest = config.RefreshRequest

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

	if args["refresh"] == "true" && !s.allowPublicRefresh(w, s.refreshKey(req, args)) {
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
	headers := make(map[string]string, len(resp.Headers)+1)
	for k, v := range resp.Headers {
		headers[k] = v
	}
	headers["Access-Control-Allow-Origin"] = "*"
	resp.Headers = headers
	sendResponse(w, resp, "", false)
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
		out, err := s.convertWithCacheContext(ctx, ck, req.Type, req.Target, func() (types.ResponseWriter, error) {
			return s.rewriteParser.Parse(ctx, input)
		}, args["refresh"] == "true")
		if err != nil {
			log.Printf("api rewriteParser error: %v", err)
			s.metrics.IncConversionError()
			return nil, statusForError(err)
		}
		return out, 0
	case config.SourceTypeRuleSet:
		target := req.Target
		if target == "" || target == "rule-set" {
			// API 无 UA 可推断，target 为空时退化为 surge-rule-set。
			target = "surge-rule-set"
		}
		ck := s.cacheKey("rule-set", target, req.URLs, args)
		input := rule.ParseInput{
			URLs:      req.URLs,
			TargetApp: target,
			Arguments: args,
		}
		out, err := s.convertWithCacheContext(ctx, ck, "rule-set", req.Target, func() (types.ResponseWriter, error) {
			return s.parseRules(ctx, input, ck)
		}, args["refresh"] == "true")
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

func (r conversionResult) Retain(n int) {
	if r.output != nil {
		if f := r.output.GetResponse().File; f != nil {
			f.Retain(n)
		}
	}
}

func (r conversionResult) Release() {
	if r.output != nil {
		if f := r.output.GetResponse().File; f != nil {
			f.Close()
		}
	}
}

type conversionResult struct {
	output    types.ResponseWriter
	fromCache bool
	stored    bool
}

// convertWithCache 统一处理磁盘缓存、并发合并与强制刷新。
// ck 为空串时跳过缓存；强制刷新复用原键。
func (s *Server) convertWithCache(ck, source, target string, parse func() (types.ResponseWriter, error), refresh ...bool) (types.ResponseWriter, error) {
	return s.convertWithCacheContext(context.Background(), ck, source, target, parse, refresh...)
}
func (s *Server) convertWithCacheContext(ctx context.Context, ck, source, target string, parse func() (types.ResponseWriter, error), refresh ...bool) (types.ResponseWriter, error) {
	if ck == "" {
		out, err := parse()
		if err != nil || out == nil {
			return out, err
		}
		return withCacheStatus(out, "BYPASS"), nil
	}
	force := len(refresh) > 0 && refresh[0]
	read := func() (types.ResponseWriter, bool) {
		rd, ok := s.cache.Get(ck)
		if !ok {
			return nil, false
		}
		return rd, true
	}
	if !force {
		if out, ok := read(); ok {
			s.metrics.IncCacheHit()
			s.metrics.IncConversion(source, target)
			return withCacheStatus(out, "HIT"), nil
		}
	}
	s.metrics.IncCacheMiss()
	var shared conversionResult
	for {
		out, err := s.flight.doContext(ctx, ck, func() (any, error) {
			// 再次检查，避免缓存填充与新一轮并发调用之间重复转换。
			if !force {
				if out, ok := read(); ok {
					return conversionResult{output: out, fromCache: true}, nil
				}
			}
			o, e := parse()
			if e != nil {
				return nil, e
			}
			if o == nil {
				return nil, errors.New("conversion returned no response")
			}
			rd := o.GetResponse()
			if rd.Incomplete || rd.Status != http.StatusOK {
				if force {
					if rd.File != nil {
						rd.File.Close()
					}
					return nil, errors.New("refresh failed: incomplete conversion")
				}
				return conversionResult{output: o}, nil
			}
			if err := s.cache.Set(ck, rd); err != nil {
				log.Printf("缓存写入失败: %v", err)
				if force {
					if rd.File != nil {
						rd.File.Close()
					}
					return nil, err
				}
				return conversionResult{output: o}, nil
			}
			return conversionResult{output: o, stored: true}, nil
		})
		if err != nil {
			return nil, err
		}
		shared = out.(conversionResult)
		// 强制请求若复用了普通请求的二次缓存检查，需要再执行一次刷新。
		if force && shared.fromCache {
			if f := shared.output.GetResponse().File; f != nil {
				f.Close()
			}
			continue
		}
		break
	}
	if force && !shared.stored {
		if f := shared.output.GetResponse().File; f != nil {
			f.Close()
		}
		return nil, errors.New("refresh failed: result was not cached")
	}
	result := shared.output
	s.metrics.IncConversion(source, target)
	state := "MISS"
	if force {
		state = "REFRESH"
	}
	return withCacheStatus(result, state), nil
}

// 缓存状态只写入当前响应，不保存到磁盘文件。
func withCacheStatus(out types.ResponseWriter, state string) types.ResponseWriter {
	rd := out.GetResponse()
	headers := make(map[string]string, len(rd.Headers)+1)
	for k, v := range rd.Headers {
		headers[k] = v
	}
	headers["X-ScriptHub-Cache"] = state
	rd.Headers = headers
	return rd
}

// statusForError 将解析器返回的错误映射为合适的 HTTP 状态码。
// 超时返回 504，磁盘额度不足或不可用返回 503，其余返回 500。
func statusForError(err error) int {
	if errors.Is(err, cache.ErrCapacity) || errors.Is(err, cache.ErrUnavailable) {
		return http.StatusServiceUnavailable
	}
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
	// JSON 保留 URL 顺序、转义分隔符，并稳定排序参数键。
	params := make(map[string]string, len(args))
	for k, v := range args {
		if k != "localtext" && k != "refresh" && k != "type" && k != "target" {
			params[k] = v
		}
	}
	key, _ := json.Marshal(struct {
		Version string
		Source  string
		Target  string
		URLs    []string
		Args    map[string]string
	}{s.cfg.Version, sourceType, target, urls, params})
	return string(key)
}

// metricsHandler 输出 Prometheus 文本格式的运行时指标。
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.Render(w)
	if s.management != nil {
		s.management.RenderMetrics(w)
	}
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

// parseRules 限制冷转换并发；磁盘不足时拒绝，避免回退全内存路径。
func (s *Server) parseRules(ctx context.Context, input rule.ParseInput, key string) (types.ResponseWriter, error) {
	select {
	case s.conversions <- struct{}{}:
		defer func() { <-s.conversions }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.cache != nil {
		workspace, err := s.cache.Workspace(key)
		if err != nil {
			return nil, err
		}
		return s.ruleParser.ParseStream(ctx, input, workspace)
	}
	// 仅显式禁用磁盘缓存时保留原有行为；初始化失败不能触发内存回退。
	if s.cfg.CacheTTL > 0 {
		return nil, cache.ErrUnavailable
	}

	return s.ruleParser.Parse(ctx, input)
}

func (s *Server) refreshKey(req convertRequest, args map[string]string) string {
	target := req.Target
	if req.Type == config.SourceTypeRuleSet && (target == "" || target == "rule-set") {
		target = config.TargetSurgeRuleSet
	}
	return s.cacheKey(req.Type, target, req.URLs, args)
}
