// Input: fmt, regexp, strings
// Output: Loon 来源格式解析器（parseLoonPlugin/parseLoonRewriteLine/parseLoonScriptLine 等）
// Pos: 业务层-重写来源解析，将 Loon .plugin 格式逐行解析为统一中间表示
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

package rewrite

import (
	"fmt"
	"strings"
)

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
