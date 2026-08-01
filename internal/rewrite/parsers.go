// Input: regexp, strings
// Output: 解析辅助函数与类型（SgArgument/PanelInfo/HostInfo）+ 跨来源共享解析辅助（parseMockLine/parseArgumentsLine/parseHostnamesFromValue 等）+ 自动识别（parseAutoDetect）+ scriptNameFromPath
// Pos: 业务层-重写来源解析的共享层，为 parsers_{qx,surge,loon}.go 提供公共辅助与自动识别入口
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

package rewrite

import (
	"regexp"
	"strings"
)

var (
	mockKVRe   = regexp.MustCompile(`\s+(?:data-type|status-code|header|data|data-path|mock-data-is-base64)\s*=`)
	mockKVReEq = regexp.MustCompile(`\s+(?:data-type|status-code|header|data|data-path|mock-data-is-base64)=`)
	argTypeRe  = regexp.MustCompile(`=\s*(input|select|switch)\s*,`)
	colonArgRe = regexp.MustCompile(`([^:,]+):(\s*".+?"|[^,]*)`)
	boolRe     = regexp.MustCompile(`^(true|false)$`)
	commaArgRe = regexp.MustCompile(`(^.*?)\s*=\s*(.*?)\s*,(.*?),\s*([^,]*\s*=.+)`)
)

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
