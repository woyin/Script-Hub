// Input: fmt, regexp, strings
// Output: Surge 来源格式解析器（parseSurgeModule/parseSurgeRewriteLine/parseSurgeScriptLine）+ Surge 专属辅助（parsePanelLine/parseHostLine/parseBodyRewriteSection/getJsField）
// Pos: 业务层-重写来源解析，将 Surge .sgmodule 格式逐行解析为统一中间表示
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

package rewrite

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	bodyRewLine = regexp.MustCompile(`^((?:http-request|http-response)(?:-jq)?)\s+?(.*?)\s+?(.*?)$`)
	panelSnRe   = regexp.MustCompile(`[=,]\s*script-name\s*=`)
	hostLineRe  = regexp.MustCompile(`^#?(?:\*|localhost|[-*?0-9a-z]+\.[-*.?0-9a-z]+)\s*=\s*(?:(?:server|script)\s*:\s*)?[\s0-9a-z:/,.]+$`)
	skipProxyRe = regexp.MustCompile(`^skip-proxy\s*=\s*(.+)`)
	realIPRe    = regexp.MustCompile(`^(?:always-)?real-ip\s*=\s*(.+)`)
)

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
//
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
