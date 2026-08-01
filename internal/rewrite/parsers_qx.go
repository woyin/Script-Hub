// Input: encoding/json, fmt, net/url, regexp, strings, internal/util
// Output: QX 来源格式解析器（parseQXRewrite/parseQXLine 等）+ QX 专属辅助（applyPinPout/qxJsonActionToBodyRewrite/isQXCronLine 等）
// Pos: 业务层-重写来源解析，将 Quantumult X 重写格式逐行解析为统一中间表示
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

var (
	jsonPathRe = regexp.MustCompile(`\.?([^\.\[\]]+)|\["([^"]*)"\]|\['([^']*)'\]|\[(\d+)\]`)
	qxCronRe   = regexp.MustCompile(`^[\d*/]+(\s+[\d*/]+){4}$`)
	qxURLRe    = regexp.MustCompile(`(https?|ftp|file)://`)
	qxTagRe    = regexp.MustCompile(`[,\s]\s*tag\s*=\s*([^\s,]+)`)
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
//
//	^https?://example.com url response-body old response-body new
//	^https?://example.com url echo-response text/plain https://example.com/data
//	^https?://example.com url reject
//	^https?://example.com url reject-dict
//	^https?://example.com url reject-img
//	^https?://example.com url 302 https://redirect.com
//	^https?://example.com url script-request-header script-path
//	^https?://example.com url script-response-body script-path
//
// parseQXRewrite parses Quantumult X rewrite format.
// QX format examples:
//
//	^https?://example.com url request-header (\r\n)User-Agent:.+(\r\n) request-header $1User-Agent: Chrome$2
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
