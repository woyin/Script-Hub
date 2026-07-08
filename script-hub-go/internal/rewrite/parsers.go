package rewrite

import (
	"fmt"
	"net/url"
	"strings"
)

const scriptHubRawURL = "https://raw.githubusercontent.com/Script-Hub-Org/Script-Hub/main"

// applyPinPout applies Pin (y, include/uncomment) and Pout (x, exclude) filters
// to raw input lines, mirroring Rewrite-Parser.js Pin0/Pout0 behavior:
//   - y: any keyword matching the line strips a leading comment marker so the
//     rule is kept (rescued from being dropped as a comment).
//   - x: any keyword matching the line prepends ";#" so the rule is excluded.
func applyPinPout(lines []string, args map[string]string) []string {
	includeItems := getArgArr(args["y"])
	excludeItems := getArgArr(args["x"])
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

// parseQXRewrite parses Quantumult X rewrite format.
// QX format examples:
//   ^https?://example.com url request-header (\r\n)User-Agent:.+(\r\n) request-header $1User-Agent: Chrome$2
//   ^https?://example.com url response-body old response-body new
//   ^https?://example.com url echo-response text/plain https://example.com/data
//   ^https?://example.com url reject
//   ^https?://example.com url reject-dict
//   ^https?://example.com url reject-img
//   ^https?://example.com url 302 https://redirect.com
//   ^https?://example.com url script-request-header script-path
//   ^https?://example.com url script-response-body script-path
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
			if rw.Type == RewriteTypeRequestHeader || rw.Type == RewriteTypeResponseHeader ||
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
		}
	}

	if rewrites, ok := sections["Rewrite"]; ok {
		for _, line := range rewrites {
			rw := parseLoonRewriteLine(line)
			if rw != nil {
				module.Rewrites = append(module.Rewrites, *rw)
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
	default:
		rw.Type = RewriteTypeURLRewrite
		if len(parts) >= 2 {
			rw.Replacement = strings.Join(parts[1:], " ")
		}
	}
	return &rw
}

// parseLoonScriptLine parses a Loon Script section line.
func parseLoonScriptLine(line string) *ParsedRewrite {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
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
