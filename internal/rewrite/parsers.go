// Input: encoding/json, fmt, net/url, regexp, strings, internal/util
// Output: QX/Surge/Loon 各来源格式解析器（parseQXRewrite/parseSurgeModule/parseLoonPlugin 等）, 解析辅助函数与类型（SgArgument/PanelInfo/HostInfo）
// Pos: 业务层-重写来源解析，将各平台原始格式逐行解析为统一中间表示
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

package rewrite

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/script-hub-org/script-hub/internal/util"
)

const scriptHubRawURL = "https://raw.githubusercontent.com/Script-Hub-Org/Script-Hub/main"

// Per-line / per-loop regexes hoisted to package level to avoid recompiling
// on every call. Names mirror the JS variable purpose from Rewrite-Parser.js.
var (
	mockKVRe    = regexp.MustCompile(`\s+(?:data-type|status-code|header|data|data-path|mock-data-is-base64)\s*=`)
	mockKVReEq  = regexp.MustCompile(`\s+(?:data-type|status-code|header|data|data-path|mock-data-is-base64)=`)
	jsonPathRe  = regexp.MustCompile(`\.?([^\.\[\]]+)|\["([^"]*)"\]|\['([^']*)'\]|\[(\d+)\]`)
	bodyRewLine = regexp.MustCompile(`^((?:http-request|http-response)(?:-jq)?)\s+?(.*?)\s+?(.*?)$`)
	argTypeRe   = regexp.MustCompile(`=\s*(input|select|switch)\s*,`)
	colonArgRe  = regexp.MustCompile(`([^:,]+):(\s*".+?"|[^,]*)`)
	boolRe      = regexp.MustCompile(`^(true|false)$`)
	commaArgRe  = regexp.MustCompile(`(^.*?)\s*=\s*(.*?)\s*,(.*?),\s*([^,]*\s*=.+)`)
	panelSnRe   = regexp.MustCompile(`[=,]\s*script-name\s*=`)
	hostLineRe  = regexp.MustCompile(`^#?(?:\*|localhost|[-*?0-9a-z]+\.[-*.?0-9a-z]+)\s*=\s*(?:(?:server|script)\s*:\s*)?[\s0-9a-z:/,.]+$`)
	skipProxyRe = regexp.MustCompile(`^skip-proxy\s*=\s*(.+)`)
	realIPRe    = regexp.MustCompile(`^(?:always-)?real-ip\s*=\s*(.+)`)
	qxCronRe    = regexp.MustCompile(`^[\d*/]+(\s+[\d*/]+){4}$`)
	qxURLRe     = regexp.MustCompile(`(https?|ftp|file)://`)
	qxTagRe     = regexp.MustCompile(`[,\s]\s*tag\s*=\s*([^\s,]+)`)
)

// applyPinPout applies Pin (y, include/uncomment) and Pout (x, exclude) filters
// to raw input lines, mirroring Rewrite-Parser.js Pin0/Pout0 behavior:
//   - y: any keyword matching the line strips a leading comment marker so the
//     rule is kept (rescued from being dropped as a comment).
//   - x: any keyword matching the line prepends ";#" so the rule is excluded.
func applyPinPout(lines []string, args map[string]string) []string {
	includeItems := util.GetArgArr(args["y"])
	excludeItems := util.GetArgArr(args["x"])
	if includeItems == nil && excludeItems == nil {
		return lines
	}
	out := make([]string, len(lines))
	for i, raw := range lines {
		line := raw
		if includeItems != nil {
			for _, item := range includeItems {
				if strings.Contains(line, item) {
					line = strings.TrimPrefix(strings.TrimLeft(line, " "), "#")
					line = strings.TrimPrefix(line, ";")
					break
				}
			}
		}
		if excludeItems != nil {
			for _, item := range excludeItems {
				if strings.Contains(line, item) {
					line = ";#" + strings.TrimLeft(line, " ")
					break
				}
			}
		}
		out[i] = line
	}
	return out
}

// getJsField extracts a `key=value` field from a Surge panel/script line using
// the same logic as Rewrite-Parser.js getJsInfo: split on the key, take the
// remainder up to the next `,` or end, trimmed of quotes.
func getJsField(line, key string) string {
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+len(key):])
	// field value ends at the next comma that separates fields
	if comma := strings.Index(rest, ","); comma >= 0 {
		rest = rest[:comma]
	}
	return strings.Trim(strings.TrimSpace(rest), `"'`)
}

// isMetaExtraLine reports whether a #! line should be preserved as extra
// metadata (excludes arguments/select/input which are handled separately).
func isMetaExtraLine(line string) bool {
	if !strings.HasPrefix(line, "#!") || !strings.Contains(line, "=") {
		return false
	}
	for _, p := range []string{"#!arguments-desc=", "#!arguments=", "#!select=", "#!input="} {
		if strings.HasPrefix(line, p) {
			return false
		}
	}
	return true
}

// parseMockLine parses a mock/echo-response line into a ParsedRewrite.
// Matches Rewrite-Parser.js getMockInfo triggers:
//   - "url echo-response CT URL"
//   - lines containing data="..." or data-type= (Surge Map Local / Loon mock-response-body)
func parseMockLine(line string) *ParsedRewrite {
	if strings.Contains(line, " echo-response ") || mockKVRe.MatchString(line) {
		rw := &ParsedRewrite{}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			rw.Pattern = strings.TrimPrefix(fields[0], "#")
			rw.Pattern = strings.Trim(rw.Pattern, `"`)
		}
		if strings.Contains(line, " echo-response ") {
			rw.Type = RewriteTypeEchoResponse
			parts := strings.Split(line, " echo-response ")
			if len(parts) >= 2 {
				rest := strings.TrimSpace(parts[1])
				restFields := strings.Fields(rest)
				if len(restFields) >= 1 {
					rw.EchoCT = restFields[0]
				}
				if len(restFields) >= 2 {
					rw.EchoURL = strings.Join(restFields[1:], " ")
				}
			}
			return rw
		}
		// data-type/data/data-path form
		rw.Type = RewriteTypeMock
		if strings.Contains(line, " mock-response-body") {
			rw.MockIsLoon = true
		}
		if strings.Contains(line, " mock-request-body") {
			rw.Type = RewriteTypeMockRequestBody
		}
		rw.MockData = unquoteField(mockField(line, "data="))
		rw.MockDataPath = unquoteField(mockField(line, "data-path="))
		rw.MockType = mockField(line, "data-type=")
		if rw.MockType == "" {
			rw.MockType = "file"
		}
		rw.MockStatus = mockField(line, "status-code=")
		rw.MockHeader = unquoteField(mockField(line, "header="))
		if v := mockField(line, "mock-data-is-base64="); v == "true" {
			rw.MockBase64 = true
		}
		// mockurl = data || datapath
		if rw.MockData != "" {
			rw.EchoURL = rw.MockData
		} else if rw.MockDataPath != "" {
			rw.EchoURL = rw.MockDataPath
		}
		// Loon data-type → Content-Type mapping
		if rw.MockIsLoon && rw.MockHeader == "" {
			rw.MockHeader = loonMockContentType(rw.MockType)
		}
		return rw
	}
	return nil
}

// mockField extracts a `key=value` field from a mock line (value up to next space
// that starts a new key= or end of line), with surrounding quotes stripped.
func mockField(line, key string) string {
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimLeft(line[idx+len(key):], " ")
	// value ends at the next " key=" pattern or end
	end := len(rest)
	if m := mockKVReEq.FindStringIndex(rest); m != nil {
		end = m[0]
	}
	return strings.Trim(strings.TrimSpace(rest[:end]), `"`)
}

func unquoteField(s string) string {
	return strings.Trim(s, `"`)
}

// loonMockContentType maps a Loon mock data-type to a Content-Type header,
// matching Rewrite-Parser.js getMockInfo switch.
func loonMockContentType(dt string) string {
	switch dt {
	case "json":
		return "Content-Type:application/json"
	case "text", "plain":
		return "Content-Type:text/plain"
	case "css":
		return "Content-Type:text/css"
	case "html":
		return "Content-Type:text/html"
	case "javascript":
		return "Content-Type:text/javascript"
	case "png":
		return "Content-Type:image/png"
	case "gif":
		return "Content-Type:image/gif"
	case "jpeg":
		return "Content-Type:image/jpeg"
	case "tiff":
		return "Content-Type:image/tiff"
	case "svg":
		return "Content-Type:image/svg+xml"
	case "mp4":
		return "Content-Type:video/mp4"
	case "form-data":
		return "Content-Type:application/x-www-form-urlencoded"
	}
	return ""
}

// parseJsonPath splits a dotted/bracket JSON path into a jq path array string,
// mirroring Rewrite-Parser.js parseJsonPath. Returns a JSON array literal.
// RE2 has no backreferences, so quoted-bracket forms are matched explicitly
// for single and double quotes.
func parseJsonPath(path string) string {
	path = strings.TrimSpace(path)
	var parts []string
	for _, m := range jsonPathRe.FindAllStringSubmatch(path, -1) {
		switch {
		case m[1] != "":
			parts = append(parts, jsonString(m[1]))
		case m[2] != "":
			parts = append(parts, jsonString(m[2]))
		case m[3] != "":
			parts = append(parts, jsonString(m[3]))
		case m[4] != "":
			parts = append(parts, m[4])
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// jsonString returns a JSON-quoted string.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// parseBodyRewriteSection parses a Surge [Body Rewrite] section into entries.
func parseBodyRewriteSection(lines []string) []BodyRewriteEntry {
	var entries []BodyRewriteEntry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		m := bodyRewLine.FindStringSubmatch(line)
		if len(m) == 4 {
			entries = append(entries, BodyRewriteEntry{Type: m[1], Regex: m[2], Value: m[3]})
		}
	}
	return entries
}

// qxJsonActionToBodyRewrite converts a QX json-add/del/replace action into
// a BodyRewriteEntry with jq expressions, mirroring Rewrite-Parser.js logic.
func qxJsonActionToBodyRewrite(pattern, httpType, action, suffix string) *BodyRewriteEntry {
	jqType := httpType + "-jq"
	fields := strings.Fields(suffix)

	if strings.Contains(action, "json-del") {
		// json-del: each field is a key to delete
		var jqExprs []string
		for _, f := range fields {
			paths := parseJsonPath(f)
			jqExprs = append(jqExprs, `'delpaths([`+paths+`])'`)
		}
		return &BodyRewriteEntry{Type: jqType, Regex: pattern, Value: strings.Join(jqExprs, " | ")}
	}

	if strings.Contains(action, "json-add") || strings.Contains(action, "json-replace") {
		// json-add/replace: key value pairs
		var jqExprs []string
		for i := 0; i+1 < len(fields); i += 2 {
			key := fields[i]
			val := fields[i+1]
			paths := parseJsonPath(key)
			if strings.Contains(action, "json-replace") {
				// conditional: if parent has key then setpath
				parent := paths
				if lastComma := strings.LastIndex(parent, ","); lastComma >= 0 {
					parent = parent[:lastComma] + "]"
				} else {
					parent = "[]"
				}
				lastKey := key
				if dot := strings.LastIndex(key, "."); dot >= 0 {
					lastKey = key[dot+1:]
				}
				lastKey = strings.Trim(lastKey, `[]'"`)
				numCheck := ""
				if _, err := fmt.Sscanf(lastKey, "%d"); err == nil {
					numCheck = lastKey
				} else {
					numCheck = `"` + lastKey + `"`
				}
				jqExprs = append(jqExprs, `'if (getpath(`+parent+`) | has(`+numCheck+`)) then (setpath(`+paths+`; `+val+`)) else . end'`)
			} else {
				// json-add: setpath
				jqExprs = append(jqExprs, `'setpath(`+paths+`; `+val+`)'`)
			}
		}
		if len(jqExprs) == 0 {
			return nil
		}
		return &BodyRewriteEntry{Type: jqType, Regex: pattern, Value: strings.Join(jqExprs, " | ")}
	}

	return nil
}

// parseHostnamesFromValue extracts comma-separated hostnames from a value string,
// stripping %APPEND%/%INSERT% method markers.
func parseHostnamesFromValue(val string) []string {
	val = strings.TrimSpace(val)
	val = strings.ReplaceAll(val, "%APPEND%", "")
	val = strings.ReplaceAll(val, "%INSERT%", "")
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseArgumentsLine parses Surge #!arguments lines into SgArgument entries.
// Supports two forms:
//   - "#!arguments=key:value,key2:value2" (colon-separated)
//   - "key=type,value,tag=..." (comma-separated, non-#! line)
func parseArgumentsLine(line string) []SgArgument {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "#!arguments") {
		idx := strings.Index(trimmed, "#!arguments")
		rest := trimmed[idx:]
		// strip "#!arguments" + optional "="
		rest = strings.TrimPrefix(rest, "#!arguments")
		rest = strings.TrimPrefix(strings.TrimLeft(rest, " "), "=")
		rest = strings.TrimLeft(rest, " ")
		return parseColonArguments(rest)
	}
	// Non-#! line form: key = type,value,tag=...
	if !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "=") {
		if argTypeRe.MatchString(trimmed) {
			return parseCommaArgument(trimmed)
		}
	}
	return nil
}

func parseColonArguments(s string) []SgArgument {
	var args []SgArgument
	for _, m := range colonArgRe.FindAllStringSubmatch(s, -1) {
		key := strings.TrimSpace(m[1])
		val := strings.TrimSpace(m[2])
		key = strings.Trim(key, `"`)
		val = strings.Trim(val, `"`)
		typ := "input"
		if boolRe.MatchString(val) {
			typ = "switch"
		}
		args = append(args, SgArgument{Key: key, Value: val, Type: typ, Tag: "tag=" + key + ", desc=" + key})
	}
	return args
}

func parseCommaArgument(s string) []SgArgument {
	m := commaArgRe.FindStringSubmatch(s)
	if len(m) < 5 {
		return nil
	}
	return []SgArgument{{
		Key:   strings.TrimSpace(m[1]),
		Type:  strings.TrimSpace(m[2]),
		Value: strings.TrimSpace(m[3]),
		Tag:   strings.TrimSpace(m[4]),
	}}
}

// JS trigger: /[=,]\s*script-name\s*=.+/
func parsePanelLine(line string) *PanelInfo {
	leadingTemplateIsNameOnly := false
	var toggleKey string
	if lt := TakeLeadingTemplate(line); lt != nil {
		leadingTemplateIsNameOnly = strings.HasPrefix(strings.TrimSpace(lt.Rest), "=")
		if !leadingTemplateIsNameOnly {
			toggleKey = lt.Key
		}
		line = lt.Rest
		if leadingTemplateIsNameOnly {
			line = lt.Key + line
		}
	}

	if !panelSnRe.MatchString(line) {
		return nil
	}
	name := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
	name = strings.TrimPrefix(name, "#")
	return &PanelInfo{
		Name:        name,
		Title:       getJsField(line, "title="),
		Content:     getJsField(line, "content="),
		Style:       getJsField(line, "style="),
		ScriptName:  getJsField(line, "script-name="),
		UpdateTimer: getJsField(line, "update-interval="),
		ToggleKey:   toggleKey,
		Raw:         line,
	}
}

// parseHostLine parses a Surge [Host] line: domain = value.
// JS trigger: /^#?(?:\*|localhost|[-*?0-9a-z]+\.[-*.?0-9a-z]+)\s*=\s*(?:server\s*:\s*|script\s*:\s*)?[\s0-9a-z:/,.]+$/
func parseHostLine(line string) *HostInfo {
	if !hostLineRe.MatchString(line) {
		return nil
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) < 2 {
		return nil
	}
	return &HostInfo{
		Domain: strings.TrimSpace(strings.TrimPrefix(parts[0], "#")),
		Value:  strings.TrimSpace(parts[1]),
		Raw:    line,
	}
}

// parseQXRewrite parses Quantumult X rewrite format.
// QX format examples:
//
//	^https?://example.com url request-header (\r\n)User-Agent:.+(\r\n) request-header $1User-Agent: Chrome$2
//	^https?://example.com url response-body old response-body new
//	^https?://example.com url echo-response text/plain https://example.com/data
//	^https?://example.com url reject
//	^https?://example.com url reject-dict
//	^https?://example.com url reject-img
//	^https?://example.com url 302 https://redirect.com
//	^https?://example.com url script-request-header script-path
//	^https?://example.com url script-response-body script-path
func (p *Parser) parseQXRewrite(content string, args map[string]string) []ParsedModule {
	var module ParsedModule
	module.Name = "QX-Rewrite"

	lines := strings.Split(content, "\n")
	lines = applyPinPout(lines, args)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		rw := parseQXLine(line)
		if rw != nil {
			if rw.Type == RewriteTypeBodyRewrite && rw.BodyRewrite != nil {
				module.BodyRewrites = append(module.BodyRewrites, *rw.BodyRewrite)
			} else if rw.Type == RewriteTypeRequestHeader || rw.Type == RewriteTypeResponseHeader ||
				rw.Type == RewriteTypeRequestBody || rw.Type == RewriteTypeResponseBody {
				rw = qxRewriteToScript(rw, i)
				module.Scripts = append(module.Scripts, *rw)
			} else if rw.Type == RewriteTypeScript {
				module.Scripts = append(module.Scripts, *rw)
			} else {
				module.Rewrites = append(module.Rewrites, *rw)
			}
			hostnames := extractHostnames(rw.Pattern)
			module.MITM = append(module.MITM, hostnames...)
		}
	}
	module.MITM = uniqueStrings(module.MITM)
	return []ParsedModule{module}
}

// qxRewriteToScript converts a QX header/body rewrite into a script entry
// matching the original JS behavior (getQxReInfo).
func qxRewriteToScript(rw *ParsedRewrite, lineNum int) *ParsedRewrite {
	isHeader := rw.Type == RewriteTypeRequestHeader || rw.Type == RewriteTypeResponseHeader
	isBody := rw.Type == RewriteTypeRequestBody || rw.Type == RewriteTypeResponseBody

	script := &ParsedRewrite{
		Type:    RewriteTypeScript,
		Pattern: rw.Pattern,
		Timeout: 30,
	}

	if isHeader {
		script.ScriptType = "http-request"
		if rw.Type == RewriteTypeResponseHeader {
			script.ScriptType = "http-response"
		}
		script.ScriptPath = scriptHubRawURL + "/scripts/replace-header.js"
		script.RequiresBody = false
		// argument = url.QueryEscape(match + "->" + replace)
		script.Arguments = url.QueryEscape(rw.MatchPart + "->" + rw.ReplacePart)
		// Generate a script name
		script.Replacement = "replaceHeader"
	} else if isBody {
		script.ScriptType = "http-request"
		if rw.Type == RewriteTypeResponseBody {
			script.ScriptType = "http-response"
		}
		script.ScriptPath = scriptHubRawURL + "/scripts/replace-body.js"
		script.RequiresBody = true
		script.Arguments = url.QueryEscape(rw.MatchPart + "->" + rw.ReplacePart)
		script.Replacement = "replaceBody"
	}

	return script
}

// parseQXLine parses a single QX rewrite line.
// QX format: PATTERN url TYPE [MATCH TYPE REPLACEMENT] or PATTERN url TYPE [SCRIPT_PATH]
func parseQXLine(line string) *ParsedRewrite {
	// Remove leading comment markers
	cleanLine := strings.TrimLeft(line, "#;")

	// Try QX Cron line first: cron_expression http(s)://...
	// Pattern: "5 * * * * https://example.com/script.js, tag=xxx"
	if isQXCronLine(cleanLine) {
		return parseQXCronLine(cleanLine)
	}

	// Split on " url " to get pattern and the rest
	urlIdx := strings.Index(cleanLine, " url ")
	if urlIdx < 0 {
		return nil
	}
	pattern := strings.TrimSpace(cleanLine[:urlIdx])
	rest := strings.TrimSpace(cleanLine[urlIdx+5:]) // after " url "

	if pattern == "" || rest == "" {
		return nil
	}

	rw := &ParsedRewrite{Pattern: pattern}

	// Determine the rewrite type from the first word of rest
	// Possible: request-header, response-header, request-body, response-body,
	//           echo-response, reject, reject-dict, reject-img,
	//           script-request-body, script-request-header, script-response-body, script-response-header,
	//           302, 307 (redirect types)
	words := strings.Fields(rest)
	if len(words) == 0 {
		return nil
	}

	rwType := words[0]

	switch rwType {
	case "request-header", "response-header":
		return parseQXHeaderBodyLine(rw, rwType, rest)

	case "request-body", "response-body":
		return parseQXHeaderBodyLine(rw, rwType, rest)

	case "request-body-json-jq", "response-body-json-jq":
		// QX: ^pattern url response-body-json-jq 'jq_expr'
		rw.Type = RewriteTypeBodyRewrite
		jqVal := strings.Join(words[1:], " ")
		if !strings.HasPrefix(jqVal, "'") {
			jqVal = "'" + jqVal + "'"
		}
		rw.BodyRewrite = &BodyRewriteEntry{
			Type:  "http-" + strings.TrimSuffix(strings.TrimPrefix(rwType, "request-"), "request-") + "-jq",
			Regex: rw.Pattern,
			Value: jqVal,
		}
		// Derive correct type: request-body-json-jq → http-request-jq
		if strings.HasPrefix(rwType, "request") {
			rw.BodyRewrite.Type = "http-request-jq"
		} else {
			rw.BodyRewrite.Type = "http-response-jq"
		}
		return rw

	case "jsonjq-request-body", "jsonjq-response-body":
		// QX alternate form: ^pattern url jsonjq-response-body 'jq_expr'
		rw.Type = RewriteTypeBodyRewrite
		jqVal := strings.Join(words[1:], " ")
		if !strings.HasPrefix(jqVal, "'") {
			jqVal = "'" + jqVal + "'"
		}
		if strings.Contains(rwType, "request") {
			rw.BodyRewrite = &BodyRewriteEntry{Type: "http-request-jq", Regex: rw.Pattern, Value: jqVal}
		} else {
			rw.BodyRewrite = &BodyRewriteEntry{Type: "http-response-jq", Regex: rw.Pattern, Value: jqVal}
		}
		return rw

	case "request-body-replace-regex", "response-body-replace-regex",
		"request-body-json-add", "request-body-json-del", "request-body-json-replace",
		"response-body-json-add", "response-body-json-del", "response-body-json-replace":
		// QX body rewrite with json actions or replace-regex
		rw.Type = RewriteTypeBodyRewrite
		suffix := strings.Join(words[1:], " ")
		httpType := "http-request"
		if strings.HasPrefix(rwType, "response") {
			httpType = "http-response"
		}
		action := rwType // e.g. response-body-json-add
		if strings.Contains(action, "json-add") || strings.Contains(action, "json-del") || strings.Contains(action, "json-replace") {
			// Convert json-add/del/replace to jq expressions
			rw.BodyRewrite = qxJsonActionToBodyRewrite(rw.Pattern, httpType, action, suffix)
		} else {
			// replace-regex: straightforward
			rw.BodyRewrite = &BodyRewriteEntry{Type: httpType, Regex: rw.Pattern, Value: suffix}
		}
		return rw

	case "echo-response":
		rw.Type = RewriteTypeEchoResponse
		// Format: echo-response CONTENT_TYPE ECHO_URL
		// The content type is words[1], echo URL is the rest after it
		if len(words) >= 3 {
			rw.EchoCT = words[1]
			rw.EchoURL = strings.Join(words[2:], " ")
			rw.Replacement = rw.EchoCT
		} else if len(words) >= 2 {
			rw.EchoCT = words[1]
			rw.EchoURL = ""
			rw.Replacement = rw.EchoCT
		}
		return rw

	case "reject":
		rw.Type = RewriteTypeReject
		return rw
	case "reject-dict":
		rw.Type = RewriteTypeRejectDict
		return rw
	case "reject-img":
		rw.Type = RewriteTypeRejectImg
		return rw
	case "reject-tinygif":
		rw.Type = RewriteTypeRejectTinyGif
		return rw
	case "reject-200":
		rw.Type = RewriteTypeReject200
		return rw
	case "reject-array":
		rw.Type = RewriteTypeRejectArray
		return rw
	case "reject-video":
		rw.Type = RewriteTypeRejectVideo
		return rw
	case "reject-drop":
		rw.Type = RewriteTypeRejectDrop
		return rw

	case "302", "307":
		rw.Type = RewriteTypeURLRewrite
		if len(words) >= 2 {
			rw.Replacement = words[0] + " " + words[1]
		}
		return rw

	case "script-request-header":
		rw.Type = RewriteTypeScript
		rw.ScriptType = "http-request"
		rw.RequiresBody = false
		if len(words) >= 2 {
			rw.ScriptPath = strings.Join(words[1:], " ")
			rw.Replacement = scriptNameFromPath(rw.ScriptPath)
		}
		rw.Timeout = 30
		return rw
	case "script-request-body":
		rw.Type = RewriteTypeScript
		rw.ScriptType = "http-request"
		rw.RequiresBody = true
		rw.BodyType = "request-body"
		if len(words) >= 2 {
			rw.ScriptPath = strings.Join(words[1:], " ")
			rw.Replacement = scriptNameFromPath(rw.ScriptPath)
		}
		rw.Timeout = 30
		return rw
	case "script-response-header":
		rw.Type = RewriteTypeScript
		rw.ScriptType = "http-response"
		rw.RequiresBody = false
		if len(words) >= 2 {
			rw.ScriptPath = strings.Join(words[1:], " ")
			rw.Replacement = scriptNameFromPath(rw.ScriptPath)
		}
		rw.Timeout = 30
		return rw
	case "script-response-body":
		rw.Type = RewriteTypeScript
		rw.ScriptType = "http-response"
		rw.RequiresBody = true
		rw.BodyType = "response-body"
		if len(words) >= 2 {
			rw.ScriptPath = strings.Join(words[1:], " ")
			rw.Replacement = scriptNameFromPath(rw.ScriptPath)
		}
		rw.Timeout = 30
		return rw

	default:
		// Try as URL rewrite: PATTERN url TARGET_URL
		rw.Type = RewriteTypeURLRewrite
		if len(words) >= 1 {
			rw.Replacement = strings.Join(words, " ")
		}
		return rw
	}
}

// parseQXHeaderBodyLine parses QX header/body rewrite lines.
// Format: TYPE MATCH TYPE REPLACE  (type keyword appears twice as separator)
// Example: request-header (\r\n)User-Agent:.+(\r\n) request-header $1User-Agent: Chrome$2
// Example: response-body old_string response-body new_string
func parseQXHeaderBodyLine(rw *ParsedRewrite, rwType string, rest string) *ParsedRewrite {
	switch rwType {
	case "request-header":
		rw.Type = RewriteTypeRequestHeader
	case "response-header":
		rw.Type = RewriteTypeResponseHeader
	case "request-body":
		rw.Type = RewriteTypeRequestBody
		rw.RequiresBody = true
		rw.BodyType = "request-body"
	case "response-body":
		rw.Type = RewriteTypeResponseBody
		rw.RequiresBody = true
		rw.BodyType = "response-body"
	}

	// The type keyword appears twice: "request-header MATCH request-header REPLACE"
	// Split on the second occurrence of the type keyword
	secondIdx := strings.Index(rest, " "+rwType+" ")
	if secondIdx >= 0 {
		// Everything between first type keyword and second type keyword is the match part
		matchPart := strings.TrimSpace(rest[len(rwType):secondIdx])
		// Everything after the second type keyword is the replace part
		replacePart := strings.TrimSpace(rest[secondIdx+len(rwType)+2:])
		rw.MatchPart = matchPart
		rw.ReplacePart = replacePart
		rw.Replacement = matchPart + "->" + replacePart
	} else {
		// No second type keyword — might be a simpler format or just the type alone
		remaining := strings.TrimSpace(rest[len(rwType):])
		if remaining != "" {
			rw.MatchPart = remaining
			rw.Replacement = remaining
		}
	}

	return rw
}

// --- Surge Module Parser ---

// parseSurgeModule parses Surge .sgmodule format.
func (p *Parser) parseSurgeModule(content string, args map[string]string) []ParsedModule {
	var module ParsedModule
	module.Name = "Surge-Module"
	sections := parseSurgeSections(content)

	// Parse name/desc from #! directives
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#!name=") {
			module.Name = strings.TrimPrefix(line, "#!name=")
		} else if strings.HasPrefix(line, "#!desc=") {
			module.Desc = strings.TrimPrefix(line, "#!desc=")
		} else if strings.HasPrefix(line, "#!icon=") {
			module.Icon = strings.TrimPrefix(line, "#!icon=")
		} else if strings.HasPrefix(line, "#!category=") {
			module.Category = strings.TrimPrefix(line, "#!category=")
		} else if strings.HasPrefix(line, "#!keyword=") {
			module.Keyword = strings.TrimPrefix(line, "#!keyword=")
		} else if isMetaExtraLine(line) {
			module.MetaExtra = append(module.MetaExtra, line)
		}
		// Parse #!arguments template system
		if m := parseArgumentsLine(line); m != nil {
			module.SgArg = append(module.SgArg, m...)
		}
	}

	// [URL Rewrite] section
	for _, sectionName := range []string{"URL Rewrite", "Rewrite"} {
		if rewrites, ok := sections[sectionName]; ok {
			for _, line := range rewrites {
				rw := parseSurgeRewriteLine(line)
				if rw != nil {
					module.Rewrites = append(module.Rewrites, *rw)
					module.MITM = append(module.MITM, extractHostnames(rw.Pattern)...)
				}
			}
		}
	}

	// [Header Rewrite] section
	if headerRewrites, ok := sections["Header Rewrite"]; ok {
		for _, line := range headerRewrites {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			rw := ParsedRewrite{
				Type:        RewriteTypeHeaderRewrite,
				Replacement: line, // Store full line for pass-through
			}
			// Extract pattern from header rewrite line: PATTERN header-rewrite ...
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				rw.Pattern = parts[0]
			}
			module.Rewrites = append(module.Rewrites, rw)
		}
	}

	// [Map Local] section
	if mapLocals, ok := sections["Map Local"]; ok {
		for _, line := range mapLocals {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// Try to parse as a structured mock entry; fall back to pass-through
			if mr := parseMockLine(line); mr != nil && mr.Type == RewriteTypeMock {
				module.Rewrites = append(module.Rewrites, *mr)
				continue
			}
			rw := ParsedRewrite{
				Type:        RewriteTypeMapLocal,
				Replacement: line, // Store full line for pass-through
			}
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				rw.Pattern = parts[0]
			}
			module.Rewrites = append(module.Rewrites, rw)
		}
	}

	// [Script] section
	if scripts, ok := sections["Script"]; ok {
		for _, line := range scripts {
			rw := parseSurgeScriptLine(line)
			if rw != nil {
				module.Scripts = append(module.Scripts, *rw)
				module.MITM = append(module.MITM, extractHostnames(rw.Pattern)...)
			}
		}
	}

	// [Panel] section (Surge)
	if panels, ok := sections["Panel"]; ok {
		for _, line := range panels {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if p := parsePanelLine(line); p != nil {
				module.Panels = append(module.Panels, *p)
			}
		}
	}

	// [Host] section (Surge)
	if hosts, ok := sections["Host"]; ok {
		for _, line := range hosts {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if h := parseHostLine(line); h != nil {
				module.Hosts = append(module.Hosts, *h)
			}
		}
	}

	// [Body Rewrite] section (Surge)
	if bodyRewrites, ok := sections["Body Rewrite"]; ok {
		module.BodyRewrites = append(module.BodyRewrites, parseBodyRewriteSection(bodyRewrites)...)
	}

	// Extract skip-proxy / real-ip / always-real-ip from [Host] or non-section lines
	// These are collected into ParsedModule.SkipProxy / RealIP for [General] output
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if m := skipProxyRe.FindStringSubmatch(line); len(m) == 2 {
			module.SkipProxy = append(module.SkipProxy, parseHostnamesFromValue(m[1])...)
		} else if m := realIPRe.FindStringSubmatch(line); len(m) == 2 {
			module.RealIP = append(module.RealIP, parseHostnamesFromValue(m[1])...)
		}
	}

	// [Rule] section
	if rules, ok := sections["Rule"]; ok {
		for _, line := range rules {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				module.Rules = append(module.Rules, line)
			}
		}
	}

	// [MITM] section
	if mitm, ok := sections["MITM"]; ok {
		module.MITM = append(module.MITM, parseMITMSection(mitm)...)
		// Detect %INSERT% or %APPEND% method from hostname line
		for _, line := range mitm {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "hostname") {
				if strings.Contains(line, "%INSERT%") {
					module.HNAddMethod = "%INSERT%"
				} else {
					module.HNAddMethod = "%APPEND%"
				}
			}
		}
	}

	module.MITM = uniqueStrings(module.MITM)
	return []ParsedModule{module}
}

// parseSurgeRewriteLine parses a Surge URL Rewrite line.
func parseSurgeRewriteLine(line string) *ParsedRewrite {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil
	}

	rw := &ParsedRewrite{
		Pattern: parts[0],
	}

	// Check for reject types
	switch parts[1] {
	case "reject":
		rw.Type = RewriteTypeReject
		return rw
	case "reject-dict":
		rw.Type = RewriteTypeRejectDict
		return rw
	case "reject-img":
		rw.Type = RewriteTypeRejectImg
		return rw
	case "reject-tinygif":
		rw.Type = RewriteTypeRejectTinyGif
		return rw
	case "reject-200":
		rw.Type = RewriteTypeReject200
		return rw
	case "reject-array":
		rw.Type = RewriteTypeRejectArray
		return rw
	case "reject-video":
		rw.Type = RewriteTypeRejectVideo
		return rw
	case "reject-drop":
		rw.Type = RewriteTypeRejectDrop
		return rw
	default:
		rw.Type = RewriteTypeURLRewrite
		rw.Replacement = strings.Join(parts[1:], " ")
		return rw
	}
}

// parseSurgeScriptLine parses a Surge Script section line.
func parseSurgeScriptLine(line string) *ParsedRewrite {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}

	leadingTemplateIsNameOnly := false
	if lt := TakeLeadingTemplate(line); lt != nil {
		leadingTemplateIsNameOnly = strings.HasPrefix(strings.TrimSpace(lt.Rest), "=")
		line = lt.Rest
		if leadingTemplateIsNameOnly {
			// The template key IS the name; rest is "= config..."
			line = lt.Key + line
		}
	}

	parts := strings.SplitN(line, "=", 2)
	if len(parts) < 2 {
		return nil
	}
	scriptName := strings.TrimSpace(parts[0])
	configStr := strings.TrimSpace(parts[1])
	rw := ParsedRewrite{
		Type:        RewriteTypeScript,
		Replacement: scriptName, // Store script name
	}

	configParts := strings.Split(configStr, ",")
	for _, cp := range configParts {
		cp = strings.TrimSpace(cp)
		kv := strings.SplitN(cp, "=", 2)
		if len(kv) == 2 {
			key := strings.TrimSpace(kv[0])
			val := strings.TrimSpace(kv[1])
			switch key {
			case "type":
				rw.ScriptType = val
			case "pattern":
				rw.Pattern = val
			case "script-path":
				rw.ScriptPath = val
			case "timeout":
				fmt.Sscanf(val, "%d", &rw.Timeout)
			case "requires-body":
				rw.RequiresBody = val == "1" || val == "true"
			case "argument":
				rw.Arguments = val
			case "max-size":
				rw.MaxSize = val
			case "event-name":
				rw.EventName = val
			case "binary-body-mode":
				rw.BinaryBody = val == "1" || val == "true"
			case "wake-system":
				rw.WakeSystem = val == "1" || val == "true"
			case "ability":
				rw.Ability = val
			case "engine":
				rw.Engine = val
			case "enable":
				rw.Enable = val == "1" || val == "true"
			case "script-update-interval":
				rw.ScriptUpdateInterval = val
			case "img-url":
				rw.ImgURL = val
			case "tag":
				rw.Tag = val
			case "cronexp", "cronexpr":
				rw.CronExp = val
			case "debug":
				rw.Debug = val
			case "desc":
				rw.Desc = val
			}
		} else {
			if cp == "http-request" || cp == "http-response" {
				rw.ScriptType = cp
			}
		}
	}

	if rw.Timeout == 0 {
		rw.Timeout = 30
	}
	return &rw
}

// --- Loon Plugin Parser ---

// parseLoonPlugin parses Loon .plugin format.
func (p *Parser) parseLoonPlugin(content string, args map[string]string) []ParsedModule {
	var module ParsedModule
	module.Name = "Loon-Plugin"
	sections := parseSurgeSections(content) // Same section-based format

	// Parse name/desc from #! directives
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#!name=") {
			module.Name = strings.TrimPrefix(line, "#!name=")
		} else if strings.HasPrefix(line, "#!desc=") {
			module.Desc = strings.TrimPrefix(line, "#!desc=")
		} else if strings.HasPrefix(line, "#!icon=") {
			module.Icon = strings.TrimPrefix(line, "#!icon=")
		} else if strings.HasPrefix(line, "#!category=") {
			module.Category = strings.TrimPrefix(line, "#!category=")
		} else if strings.HasPrefix(line, "#!keyword=") {
			module.Keyword = strings.TrimPrefix(line, "#!keyword=")
		} else if isMetaExtraLine(line) {
			// Preserve other #!key=value metadata (e.g. Loon interactive buttons)
			module.MetaExtra = append(module.MetaExtra, line)
		} else if strings.HasPrefix(line, "#!select=") || strings.HasPrefix(line, "#!input=") {
			if entry := ParseInputBox(line); entry != nil {
				module.ModInputBox = append(module.ModInputBox, *entry)
			}
		}
		// Parse #!arguments template system
		if m := parseArgumentsLine(line); m != nil {
			module.SgArg = append(module.SgArg, m...)
		}
	}

	if rewrites, ok := sections["Rewrite"]; ok {
		for _, line := range rewrites {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// Loon mock-response-body lines: parse as mock first
			if mr := parseMockLine(line); mr != nil && (mr.Type == RewriteTypeMock || mr.Type == RewriteTypeMockRequestBody) {
				module.Rewrites = append(module.Rewrites, *mr)
				module.MITM = append(module.MITM, extractHostnames(mr.Pattern)...)
				continue
			}
			rw := parseLoonRewriteLine(line)
			if rw != nil {
				if rw.Type == RewriteTypeBodyRewrite && rw.BodyRewrite != nil {
					module.BodyRewrites = append(module.BodyRewrites, *rw.BodyRewrite)
				} else {
					module.Rewrites = append(module.Rewrites, *rw)
				}
				module.MITM = append(module.MITM, extractHostnames(rw.Pattern)...)
			}
		}
	}

	if scripts, ok := sections["Script"]; ok {
		for _, line := range scripts {
			rw := parseLoonScriptLine(line)
			if rw != nil {
				module.Scripts = append(module.Scripts, *rw)
				module.MITM = append(module.MITM, extractHostnames(rw.Pattern)...)
			}
		}
	}

	if rules, ok := sections["Rule"]; ok {
		for _, line := range rules {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				module.Rules = append(module.Rules, line)
			}
		}
	}

	if mitm, ok := sections["MITM"]; ok {
		module.MITM = append(module.MITM, parseMITMSection(mitm)...)
		for _, line := range mitm {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "hostname") {
				if strings.Contains(line, "%INSERT%") {
					module.HNAddMethod = "%INSERT%"
				} else {
					module.HNAddMethod = "%APPEND%"
				}
			}
		}
	}

	module.MITM = uniqueStrings(module.MITM)
	return []ParsedModule{module}
}

// parseLoonRewriteLine parses a Loon Rewrite line.
func parseLoonRewriteLine(line string) *ParsedRewrite {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	// Loon format: ^pattern url-request-header match replace
	//              ^pattern url-response-header match replace
	//              ^pattern url-request-body old new
	//              ^pattern url-response-body old new
	//              ^pattern url-reject
	//              ^pattern TARGET_URL (URL rewrite)
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil
	}
	rw := ParsedRewrite{Pattern: parts[0]}

	rwType := parts[1]
	switch {
	case rwType == "url-request-header":
		rw.Type = RewriteTypeRequestHeader
		if len(parts) >= 4 {
			rw.MatchPart = parts[2]
			rw.ReplacePart = strings.Join(parts[3:], " ")
			rw.Replacement = rw.MatchPart + "->" + rw.ReplacePart
		} else if len(parts) >= 3 {
			rw.MatchPart = parts[2]
			rw.Replacement = rw.MatchPart
		}
	case rwType == "url-response-header":
		rw.Type = RewriteTypeResponseHeader
		if len(parts) >= 4 {
			rw.MatchPart = parts[2]
			rw.ReplacePart = strings.Join(parts[3:], " ")
			rw.Replacement = rw.MatchPart + "->" + rw.ReplacePart
		} else if len(parts) >= 3 {
			rw.MatchPart = parts[2]
			rw.Replacement = rw.MatchPart
		}
	case rwType == "url-request-body":
		rw.Type = RewriteTypeRequestBody
		rw.RequiresBody = true
		rw.BodyType = "request-body"
		if len(parts) >= 4 {
			rw.MatchPart = parts[2]
			rw.ReplacePart = strings.Join(parts[3:], " ")
			rw.Replacement = rw.MatchPart + "->" + rw.ReplacePart
		} else if len(parts) >= 3 {
			rw.MatchPart = parts[2]
			rw.Replacement = rw.MatchPart
		}
	case rwType == "url-response-body":
		rw.Type = RewriteTypeResponseBody
		rw.RequiresBody = true
		rw.BodyType = "response-body"
		if len(parts) >= 4 {
			rw.MatchPart = parts[2]
			rw.ReplacePart = strings.Join(parts[3:], " ")
			rw.Replacement = rw.MatchPart + "->" + rw.ReplacePart
		} else if len(parts) >= 3 {
			rw.MatchPart = parts[2]
			rw.Replacement = rw.MatchPart
		}
	case strings.HasPrefix(rwType, "url-reject"):
		rw.Type = RewriteTypeReject
	case strings.HasSuffix(rwType, "header-del") || strings.HasSuffix(rwType, "header-add") ||
		strings.HasSuffix(rwType, "header-replace") || strings.HasSuffix(rwType, "header-replace-regex"):
		// Loon: url-request-header-del / url-response-header-add / etc.
		return parseLoonHeaderActionLine(line, parts)
	case rwType == "request-body-replace-regex" || rwType == "response-body-replace-regex" ||
		rwType == "request-body-json-jq" || rwType == "response-body-json-jq":
		// Loon body rewrite inside [Rewrite]: <pattern> <type> <value>.
		// Map Loon type to the canonical BodyRewriteEntry.Type used by the
		// Surge [Body Rewrite] section and the QX-derived IR.
		var canonical string
		switch rwType {
		case "request-body-replace-regex":
			canonical = "http-request"
		case "response-body-replace-regex":
			canonical = "http-response"
		case "request-body-json-jq":
			canonical = "http-request-jq"
		case "response-body-json-jq":
			canonical = "http-response-jq"
		}
		value := ""
		if len(parts) >= 3 {
			value = strings.Join(parts[2:], " ")
		}
		rw.Type = RewriteTypeBodyRewrite
		rw.BodyRewrite = &BodyRewriteEntry{Type: canonical, Regex: rw.Pattern, Value: value}
		return &rw
	default:
		rw.Type = RewriteTypeURLRewrite
		if len(parts) >= 2 {
			rw.Replacement = strings.Join(parts[1:], " ")
		}
	}
	return &rw
}

// parseLoonHeaderActionLine parses a Loon header-del/add/replace/replace-regex line.
// Loon format: ^pattern url-(request|response)-header-(del|add|replace|replace-regex) key [value]
// Returns multiple ParsedRewrite entries (one per key-value pair), or a single one.
func parseLoonHeaderActionLine(line string, parts []string) *ParsedRewrite {
	if len(parts) < 3 {
		return nil
	}
	rw := ParsedRewrite{Pattern: parts[0]}
	rwType := parts[1]

	// Determine request/response and action
	isResponse := strings.Contains(rwType, "response-")
	var action string
	switch {
	case strings.HasSuffix(rwType, "header-del"):
		action = "header-del"
		rw.Type = RewriteTypeHeaderDel
	case strings.HasSuffix(rwType, "header-add"):
		action = "header-add"
		rw.Type = RewriteTypeHeaderAdd
	case strings.HasSuffix(rwType, "header-replace-regex"):
		action = "header-replace-regex"
		rw.Type = RewriteTypeHeaderReplaceRegex
	case strings.HasSuffix(rwType, "header-replace"):
		action = "header-replace"
		rw.Type = RewriteTypeHeaderReplace
	}

	prefix := "http-request"
	if isResponse {
		prefix = "http-response"
	}

	// Build the Surge-style header rewrite line
	suffix := strings.Join(parts[2:], " ")
	var headerEntries []string
	suffixParts := strings.Fields(suffix)

	switch action {
	case "header-del":
		for _, key := range suffixParts {
			headerEntries = append(headerEntries, fmt.Sprintf("%s %s %s", prefix, action, "'"+key+"'"))
		}
	case "header-add", "header-replace":
		for i := 0; i+1 < len(suffixParts); i += 2 {
			key := suffixParts[i]
			val := suffixParts[i+1]
			headerEntries = append(headerEntries, fmt.Sprintf("%s %s '%s' '%s'", prefix, action, key, val))
		}
	case "header-replace-regex":
		for i := 0; i+2 < len(suffixParts); i += 3 {
			key := suffixParts[i]
			pattern := suffixParts[i+1]
			replacement := suffixParts[i+2]
			headerEntries = append(headerEntries, fmt.Sprintf("%s %s '%s' '%s' '%s'", prefix, action, key, pattern, replacement))
		}
	}

	// Store as a header rewrite with reconstructed line(s)
	if len(headerEntries) > 0 {
		rw.Replacement = strings.Join(headerEntries, "\n")
		rw.Type = RewriteTypeHeaderRewrite
	}
	return &rw
}

// isQXCronLine checks if a QX line is a cron expression followed by a URL.
// Format: "5 * * * * https://example.com/script.js" or with tag
func isQXCronLine(line string) bool {
	// Must contain a URL somewhere
	if !strings.Contains(line, "://") {
		return false
	}
	// Must start with cron-like tokens (digits and */ and spaces)
	parts := strings.Fields(line)
	if len(parts) < 6 {
		return false
	}
	// First 5 parts should be cron fields (digits, *, /)
	cronPart := strings.Join(parts[:5], " ")
	return qxCronRe.MatchString(cronPart)
}

// parseQXCronLine parses a QX cron line into a ParsedRewrite with type=cron.
func parseQXCronLine(line string) *ParsedRewrite {
	line = strings.ReplaceAll(line, "  ", " ")
	line = strings.TrimLeft(line, "#;")

	// Split into cron expression and URL
	loc := qxURLRe.FindStringIndex(line)
	if loc == nil {
		return nil
	}

	cronPart := strings.TrimSpace(line[:loc[0]])
	urlPart := strings.TrimSpace(line[loc[0]:])

	// Extract tag from URL part
	var scriptName string
	if m := qxTagRe.FindStringSubmatch(urlPart); len(m) >= 2 {
		scriptName = m[1]
	}
	// Clean URL: remove tag and other params after the URL
	scriptURL := urlPart
	if commaIdx := strings.Index(scriptURL, ","); commaIdx >= 0 {
		scriptURL = strings.TrimSpace(scriptURL[:commaIdx])
	}

	if scriptName == "" {
		// Derive from URL filename
		lastSlash := strings.LastIndex(scriptURL, "/")
		lastDot := strings.LastIndex(scriptURL, ".")
		if lastSlash >= 0 && lastDot > lastSlash {
			scriptName = scriptURL[lastSlash+1 : lastDot]
		}
	}

	return &ParsedRewrite{
		Type:        RewriteTypeScript,
		ScriptType:  "cron",
		CronExp:     cronPart,
		ScriptPath:  scriptURL,
		Replacement: scriptName,
		Timeout:     120,
		WakeSystem:  true,
	}
}

// parseLoonScriptLine parses a Loon Script section line.
func parseLoonScriptLine(line string) *ParsedRewrite {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}

	// Loon canonical [Script] form has no leading "name =":
	//   http-response PATTERN script-path=URL, tag=NAME, requires-body=true
	//   cron "0 8 * * *" script-path=URL, tag=NAME
	// The Surge "name = config" form (parseSurgeScriptLine) is handled elsewhere;
	// here detect Loon form by the presence of "script-path=" and parse by token.
	if strings.Contains(line, "script-path=") {
		return parseLoonScriptLineDirect(line)
	}

	parts := strings.SplitN(line, "=", 2)
	if len(parts) < 2 {
		return nil
	}
	configStr := strings.TrimSpace(parts[1])
	rw := ParsedRewrite{
		Type: RewriteTypeScript,
	}

	configParts := strings.Split(configStr, ",")
	for _, cp := range configParts {
		cp = strings.TrimSpace(cp)
		kv := strings.SplitN(cp, "=", 2)
		if len(kv) == 2 {
			key := strings.TrimSpace(kv[0])
			val := strings.TrimSpace(kv[1])
			switch key {
			case "type":
				rw.ScriptType = val
			case "pattern":
				rw.Pattern = val
			case "script-path":
				rw.ScriptPath = val
			case "timeout":
				fmt.Sscanf(val, "%d", &rw.Timeout)
			case "requires-body":
				rw.RequiresBody = val == "1" || val == "true"
			case "argument":
				rw.Arguments = val
			case "script-name":
				// ignore, we generate our own
			}
		} else {
			if cp == "http-request" || cp == "http-response" {
				rw.ScriptType = cp
			}
		}
	}
	rw.Type = RewriteTypeScript
	if rw.Timeout == 0 {
		rw.Timeout = 30
	}
	return &rw
}

// parseLoonScriptLineDirect parses the canonical Loon [Script] line that has no
// leading "name =", e.g.:
//
//	http-response PATTERN script-path=URL, tag=NAME, requires-body=true
//	cron "0 8 * * *" script-path=URL, tag=NAME
//
// Pattern is taken from the token immediately before "script-path=". For cron
// lines the cron expression is read from the quoted segment.
func parseLoonScriptLineDirect(line string) *ParsedRewrite {
	rw := ParsedRewrite{Type: RewriteTypeScript}
	if strings.HasPrefix(line, "cron") {
		rw.ScriptType = "cron"
		if qi := strings.Index(line, `"`); qi >= 0 {
			if end := strings.Index(line[qi+1:], `"`); end >= 0 {
				rw.CronExp = line[qi+1 : qi+1+end]
			}
		}
	} else {
		tokens := strings.Fields(line)
		if len(tokens) >= 2 {
			rw.ScriptType = tokens[0]
		}
	}
	// Pattern: token before "script-path=" (http form only).
	if rw.ScriptType != "cron" {
		tokens := strings.Fields(line)
		for i, tok := range tokens {
			if strings.HasPrefix(tok, "script-path=") && i-1 >= 0 {
				rw.Pattern = tokens[i-1]
				break
			}
		}
	}
	// Options live after the first "script-path=" occurrence.
	optStart := strings.Index(line, "script-path=")
	configStr := line
	if optStart >= 0 {
		configStr = line[optStart:]
	}
	for _, cp := range strings.Split(configStr, ",") {
		cp = strings.TrimSpace(cp)
		kv := strings.SplitN(cp, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "script-path":
			rw.ScriptPath = val
		case "timeout":
			fmt.Sscanf(val, "%d", &rw.Timeout)
		case "requires-body":
			rw.RequiresBody = val == "1" || val == "true"
		case "argument":
			rw.Arguments = val
		case "tag":
			rw.Tag = val
			rw.Replacement = val
		case "img-url":
			rw.ImgURL = val
		case "enable", "enabled":
			rw.Enable = val == "true"
		}
	}
	if rw.Timeout == 0 {
		rw.Timeout = 30
	}
	return &rw
}

// --- Auto-detect ---

// parseAutoDetect attempts to auto-detect the source format.
func (p *Parser) parseAutoDetect(content string, args map[string]string) []ParsedModule {
	// Heuristic: check for section headers
	hasSurgeSections := strings.Contains(content, "[URL Rewrite]") ||
		strings.Contains(content, "[Script]") ||
		strings.Contains(content, "[Header Rewrite]") ||
		strings.Contains(content, "[Map Local]") ||
		strings.Contains(content, "[MITM]")
	hasRewriteSection := strings.Contains(content, "[Rewrite]")

	if hasSurgeSections || hasRewriteSection {
		if hasRewriteSection && !strings.Contains(content, "[URL Rewrite]") && !strings.Contains(content, "[Header Rewrite]") {
			// Loon uses [Rewrite] not [URL Rewrite]
			return p.parseLoonPlugin(content, args)
		}
		return p.parseSurgeModule(content, args)
	}
	// Default to QX rewrite format
	return p.parseQXRewrite(content, args)
}

// scriptNameFromPath extracts a clean script name from a script URL/path.
// "https://raw.githubusercontent.com/test/script.js" → "script"
func scriptNameFromPath(path string) string {
	if path == "" {
		return "script"
	}
	path = strings.ReplaceAll(path, `\.`, ".")
	path = strings.ReplaceAll(path, `\-`, "-")
	path = strings.ReplaceAll(path, `\_`, "_")
	idx := strings.LastIndex(path, "/")
	name := path
	if idx >= 0 && idx < len(path)-1 {
		name = path[idx+1:]
	}
	if dotIdx := strings.LastIndex(name, "."); dotIdx > 0 {
		name = name[:dotIdx]
	}
	if name == "" {
		return "script"
	}
	return name
}
