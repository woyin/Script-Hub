package rewrite

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const scriptHubRawBase = "https://raw.githubusercontent.com/Script-Hub-Org/Script-Hub/main"

type surgeOutput struct {
	URLRewrites     []string
	HeaderRewrites  []string
	Scripts         []string
	MapLocal        []string
	Rules           []string
	MITM            []string
	ForceHTTPHosts  []string
	Name            string
	Desc            string
	Icon            string
}

// convertModules converts parsed modules to the target app format.
func (p *Parser) convertModules(modules []ParsedModule, targetApp string, args map[string]string) string {
	target := strings.ToLower(targetApp)

	switch {
	case strings.Contains(target, "surge") || strings.Contains(target, "shadowrocket"):
		return p.convertToSurgeFormat(modules, target, args)
	case strings.Contains(target, "loon"):
		return p.convertToLoonFormat(modules, target, args)
	case strings.Contains(target, "stash"):
		return p.convertToStashFormat(modules, target, args)
	default:
		return p.convertToGenericFormat(modules, target, args)
	}
}

// --- Surge / Shadowrocket ---

func (p *Parser) convertToSurgeFormat(modules []ParsedModule, target string, args map[string]string) string {
	out := surgeOutput{}
	synMitm := isTrue(args["synMitm"])
	delComments := isTrue(args["del"])

	for _, mod := range modules {
		out.Name = mod.Name
		out.Desc = mod.Desc
		out.Icon = mod.Icon
		out.MITM = append(out.MITM, mod.MITM...)
		out.Rules = append(out.Rules, mod.Rules...)

		for _, rw := range mod.Rewrites {
			p.classifySurgeRewrite(rw, &out, args)
		}
		for _, rw := range mod.Scripts {
			p.classifySurgeScript(rw, &out, args)
		}
	}

	out.MITM = uniqueStrings(out.MITM)

	if synMitm {
		out.ForceHTTPHosts = append(out.ForceHTTPHosts, out.MITM...)
	}

	if delComments {
		out.URLRewrites = filterCommented(out.URLRewrites)
		out.HeaderRewrites = filterCommented(out.HeaderRewrites)
		out.Scripts = filterCommented(out.Scripts)
		out.Rules = filterCommented(out.Rules)
	}

	return p.formatSurgeOutput(out)
}

// classifySurgeRewrite classifies a rewrite rule into the correct Surge section.
// QX header/body rewrites are already converted to script entries by the parser,
// so they won't appear here from QX input. This handles: reject, URL rewrite,
// echo-response, native Header Rewrite pass-through, and Map Local pass-through.
func (p *Parser) classifySurgeRewrite(rw ParsedRewrite, out *surgeOutput, args map[string]string) {
	switch rw.Type {
	case RewriteTypeEchoResponse:
		scriptURL := scriptHubRawBase + "/scripts/echo-response.js"
		echoURL := cleanRegexEscapes(rw.EchoURL)
		echoArg := url.QueryEscape(fmt.Sprintf("type=%s&url=%s", rw.EchoCT, echoURL))
		scriptName := "echo-" + scriptNameFromPath(echoURL)
		out.Scripts = append(out.Scripts,
			fmt.Sprintf("%s = type=http-response, pattern=%s, script-path=%s, requires-body=false, max-size=0, script-update-interval=86400, timeout=30, argument=%s",
				scriptName, rw.Pattern, scriptURL, echoArg))

	case RewriteTypeReject, RewriteTypeRejectDict, RewriteTypeRejectImg,
		RewriteTypeRejectTinyGif, RewriteTypeReject200, RewriteTypeRejectArray,
		RewriteTypeRejectVideo, RewriteTypeRejectDrop:
		rejectType := "reject"
		switch rw.Type {
		case RewriteTypeRejectDict:
			rejectType = "reject-dict"
		case RewriteTypeReject200:
			rejectType = "reject-200"
		case RewriteTypeRejectImg:
			rejectType = "reject-img"
		case RewriteTypeRejectTinyGif:
			rejectType = "reject-tinygif"
		case RewriteTypeRejectArray:
			rejectType = "reject-array"
		case RewriteTypeRejectVideo:
			// JS maps reject-video to reject-tinygif on Surge/Shadowrocket
			rejectType = "reject-tinygif"
		case RewriteTypeRejectDrop:
			rejectType = "reject-drop"
		}
		out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s %s", rw.Pattern, rejectType))

	case RewriteTypeURLRewrite:
		out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s %s", rw.Pattern, rw.Replacement))

	case RewriteTypeHeaderRewrite:
		// Native Surge [Header Rewrite] pass-through
		out.HeaderRewrites = append(out.HeaderRewrites, rw.Replacement)

	case RewriteTypeMapLocal:
		// Native Surge [Map Local] pass-through
		out.MapLocal = append(out.MapLocal, rw.Replacement)

	// Fallback for non-QX sources that still have header/body rewrite types
	case RewriteTypeRequestHeader, RewriteTypeResponseHeader:
		direction := "request"
		if rw.Type == RewriteTypeResponseHeader {
			direction = "response"
		}
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			out.HeaderRewrites = append(out.HeaderRewrites,
				fmt.Sprintf("%s header-rewrite %s-header %s %s", rw.Pattern, direction, rw.MatchPart, rw.ReplacePart))
		} else if rw.Replacement != "" {
			parts := strings.SplitN(rw.Replacement, "->", 2)
			if len(parts) == 2 {
				out.HeaderRewrites = append(out.HeaderRewrites,
					fmt.Sprintf("%s header-rewrite %s-header %s %s", rw.Pattern, direction, parts[0], parts[1]))
			}
		}

	case RewriteTypeRequestBody, RewriteTypeResponseBody:
		scriptURL := scriptHubRawBase + "/scripts/replace-body.js"
		scriptType := "http-request"
		if rw.Type == RewriteTypeResponseBody {
			scriptType = "http-response"
		}
		arg := rw.Arguments
		if arg == "" {
			arg = url.QueryEscape(rw.Replacement)
		}
		out.Scripts = append(out.Scripts,
			fmt.Sprintf("body-%s = type=%s, pattern=%s, script-path=%s, requires-body=true, max-size=0, script-update-interval=86400, timeout=30, argument=%s",
				sanitizeName(rw.Pattern), scriptType, rw.Pattern, scriptURL, arg))
	}
}

// classifySurgeScript classifies a script entry into the Surge [Script] section.
func (p *Parser) classifySurgeScript(rw ParsedRewrite, out *surgeOutput, args map[string]string) {
	timeout := rw.Timeout
	if timeout == 0 {
		timeout = 30
	}
	requiresBody := 0
	if rw.RequiresBody {
		requiresBody = 1
	}
	argStr := ""
	if rw.Arguments != "" {
		argStr = fmt.Sprintf(", argument=%s", rw.Arguments)
	}

	// Use the script name from Replacement if set (e.g. "replaceHeader"),
	// otherwise generate from pattern
	scriptName := rw.Replacement
	if scriptName == "" {
		scriptName = sanitizeName(rw.Pattern)
	}

	out.Scripts = append(out.Scripts,
		fmt.Sprintf("%s = type=%s, pattern=%s, script-path=%s, requires-body=%d, max-size=0, script-update-interval=86400, timeout=%d%s",
			scriptName, rw.ScriptType, rw.Pattern, rw.ScriptPath, requiresBody, timeout, argStr))
}

// formatSurgeOutput assembles the Surge .sgmodule format.
func (p *Parser) formatSurgeOutput(out surgeOutput) string {
	var sb strings.Builder

	if out.Name != "" {
		sb.WriteString(fmt.Sprintf("#!name=%s\n", out.Name))
	}
	if out.Desc != "" {
		sb.WriteString(fmt.Sprintf("#!desc=%s\n", out.Desc))
	}
	if out.Icon != "" {
		sb.WriteString(fmt.Sprintf("#!icon=%s\n", out.Icon))
	}
	sb.WriteString("\n")

	if len(out.Rules) > 0 {
		sb.WriteString("[Rule]\n")
		for _, r := range out.Rules {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}

	if len(out.URLRewrites) > 0 {
		sb.WriteString("[URL Rewrite]\n")
		for _, r := range out.URLRewrites {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}

	if len(out.HeaderRewrites) > 0 {
		sb.WriteString("[Header Rewrite]\n")
		for _, r := range out.HeaderRewrites {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}

	if len(out.MapLocal) > 0 {
		sb.WriteString("[Map Local]\n")
		for _, r := range out.MapLocal {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}

	if len(out.Scripts) > 0 {
		sb.WriteString("[Script]\n")
		for _, s := range out.Scripts {
			sb.WriteString(s + "\n")
		}
		sb.WriteString("\n")
	}

	if len(out.MITM) > 0 {
		sb.WriteString("[MITM]\n")
		sb.WriteString("hostname = %APPEND% " + strings.Join(out.MITM, ", ") + "\n")
	}

	if len(out.ForceHTTPHosts) > 0 {
		sb.WriteString("\n[Host]\n")
		sb.WriteString("force-http-engine-hosts = " + strings.Join(out.ForceHTTPHosts, ", ") + "\n")
	}

	return sb.String()
}

// --- Loon ---

func (p *Parser) convertToLoonFormat(modules []ParsedModule, target string, args map[string]string) string {
	var rules, rewrites, scripts, mitm []string
	var name, desc, icon string
	delComments := isTrue(args["del"])

	for _, mod := range modules {
		name = mod.Name
		desc = mod.Desc
		icon = mod.Icon
		mitm = append(mitm, mod.MITM...)
		rules = append(rules, mod.Rules...)

		for _, rw := range mod.Rewrites {
			converted := p.convertLoonRewrite(rw)
			if converted != "" {
				rewrites = append(rewrites, converted)
			}
		}
		for _, rw := range mod.Scripts {
			converted := p.convertLoonScript(rw)
			if converted != "" {
				scripts = append(scripts, converted)
			}
		}
	}

	mitm = uniqueStrings(mitm)

	if delComments {
		rewrites = filterCommented(rewrites)
		scripts = filterCommented(scripts)
		rules = filterCommented(rules)
	}

	var sb strings.Builder
	if name != "" {
		sb.WriteString(fmt.Sprintf("#!name=%s\n", name))
	}
	if desc != "" {
		sb.WriteString(fmt.Sprintf("#!desc=%s\n", desc))
	}
	if icon != "" {
		sb.WriteString(fmt.Sprintf("#!icon=%s\n", icon))
	}
	sb.WriteString("\n")

	if len(rules) > 0 {
		sb.WriteString("[Rule]\n")
		for _, r := range rules {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}
	if len(rewrites) > 0 {
		sb.WriteString("[Rewrite]\n")
		for _, r := range rewrites {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}
	if len(scripts) > 0 {
		sb.WriteString("[Script]\n")
		for _, s := range scripts {
			sb.WriteString(s + "\n")
		}
		sb.WriteString("\n")
	}
	if len(mitm) > 0 {
		sb.WriteString("[MITM]\n")
		sb.WriteString("hostname = " + strings.Join(mitm, ", ") + "\n")
	}
	return sb.String()
}

// convertLoonRewrite converts a rewrite entry to Loon [Rewrite] format.
func (p *Parser) convertLoonRewrite(rw ParsedRewrite) string {
	switch rw.Type {
	case RewriteTypeRequestHeader:
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url-request-header %s %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		parts := strings.SplitN(rw.Replacement, "->", 2)
		if len(parts) == 2 {
			return fmt.Sprintf("%s url-request-header %s %s", rw.Pattern, parts[0], parts[1])
		}
		return fmt.Sprintf("%s url-request-header %s", rw.Pattern, rw.Replacement)

	case RewriteTypeResponseHeader:
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url-response-header %s %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		parts := strings.SplitN(rw.Replacement, "->", 2)
		if len(parts) == 2 {
			return fmt.Sprintf("%s url-response-header %s %s", rw.Pattern, parts[0], parts[1])
		}
		return fmt.Sprintf("%s url-response-header %s", rw.Pattern, rw.Replacement)

	case RewriteTypeRequestBody:
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url-request-body %s %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		return fmt.Sprintf("%s url-request-body %s", rw.Pattern, rw.Replacement)

	case RewriteTypeResponseBody:
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url-response-body %s %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		return fmt.Sprintf("%s url-response-body %s", rw.Pattern, rw.Replacement)

	case RewriteTypeReject, RewriteTypeRejectDict, RewriteTypeRejectImg,
		RewriteTypeRejectTinyGif, RewriteTypeReject200, RewriteTypeRejectArray,
		RewriteTypeRejectVideo, RewriteTypeRejectDrop:
		return fmt.Sprintf("%s url-reject", rw.Pattern)

	case RewriteTypeURLRewrite:
		return fmt.Sprintf("%s %s", rw.Pattern, rw.Replacement)

	case RewriteTypeHeaderRewrite:
		// Surge [Header Rewrite] -> Loon [Rewrite]
		return convertSurgeHeaderRewriteToLoon(rw.Replacement)

	case RewriteTypeEchoResponse:
		dataType := rw.EchoCT
		if dataType == "" {
			dataType = "text"
		}
		dataPath := rw.EchoURL
		if dataPath != "" {
			return fmt.Sprintf("%s mock-response-body data-type=%s data-path=%s", rw.Pattern, dataType, dataPath)
		}
		return fmt.Sprintf("%s mock-response-body data-type=%s", rw.Pattern, dataType)

	case RewriteTypeMapLocal:
		return rw.Replacement

	default:
		return ""
	}
}

// convertSurgeHeaderRewriteToLoon converts Surge [Header Rewrite] to Loon [Rewrite].
func convertSurgeHeaderRewriteToLoon(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 5 && parts[1] == "header-rewrite" {
		direction := parts[2] // request-header or response-header
		pattern := parts[0]
		match := parts[3]
		replace := strings.Join(parts[4:], " ")

		loondir := "url-request-header"
		if strings.HasPrefix(direction, "response") {
			loondir = "url-response-header"
		}
		return fmt.Sprintf("%s %s %s %s", pattern, loondir, match, replace)
	}
	// Fallback: return as-is
	return line
}

func (p *Parser) convertLoonScript(rw ParsedRewrite) string {
	timeout := rw.Timeout
	if timeout == 0 {
		timeout = 30
	}

	scriptName := rw.Replacement
	if scriptName == "" {
		scriptName = sanitizeName(rw.Pattern)
	}

	var opts []string
	opts = append(opts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
	opts = append(opts, fmt.Sprintf("timeout=%d", timeout))
	opts = append(opts, fmt.Sprintf("tag=%s", scriptName))
	if rw.RequiresBody {
		opts = append(opts, "requires-body=true")
	}
	if rw.Arguments != "" {
		opts = append(opts, fmt.Sprintf("argument=%s", rw.Arguments))
	}

	scriptType := strings.TrimPrefix(rw.ScriptType, "http-")
	return fmt.Sprintf("http-%s %s %s", scriptType, rw.Pattern, strings.Join(opts, ", "))
}

// --- Stash ---

func (p *Parser) convertToStashFormat(modules []ParsedModule, target string, args map[string]string) string {
	out := surgeOutput{}
	delComments := isTrue(args["del"])

	for _, mod := range modules {
		out.Name = mod.Name
		out.Desc = mod.Desc
		out.Icon = mod.Icon
		out.MITM = append(out.MITM, mod.MITM...)
		out.Rules = append(out.Rules, mod.Rules...)

		for _, rw := range mod.Rewrites {
			p.classifySurgeRewrite(rw, &out, args)
		}
		for _, rw := range mod.Scripts {
			p.classifySurgeScript(rw, &out, args)
		}
	}

	out.MITM = uniqueStrings(out.MITM)

	if isTrue(args["synMitm"]) {
		out.ForceHTTPHosts = append(out.ForceHTTPHosts, out.MITM...)
	}

	if delComments {
		out.URLRewrites = filterCommented(out.URLRewrites)
		out.HeaderRewrites = filterCommented(out.HeaderRewrites)
		out.Scripts = filterCommented(out.Scripts)
		out.Rules = filterCommented(out.Rules)
	}

	return p.formatStashOutput(out)
}

func (p *Parser) formatStashOutput(out surgeOutput) string {
	var sb strings.Builder

	if out.Name != "" {
		sb.WriteString(fmt.Sprintf("#!name=%s\n", out.Name))
	}
	if out.Desc != "" {
		sb.WriteString(fmt.Sprintf("#!desc=%s\n", out.Desc))
	}
	if out.Icon != "" {
		sb.WriteString(fmt.Sprintf("#!icon=%s\n", out.Icon))
	}
	sb.WriteString("\n")

	if len(out.Rules) > 0 {
		sb.WriteString("rules:\n")
		for _, r := range out.Rules {
			sb.WriteString("  - " + r + "\n")
		}
		sb.WriteString("\n")
	}

	hasHTTP := len(out.MITM) > 0 || len(out.HeaderRewrites) > 0 ||
		len(out.URLRewrites) > 0 || len(out.Scripts) > 0
	if hasHTTP {
		sb.WriteString("http:\n")

		if len(out.MITM) > 0 {
			sb.WriteString("  mitm:\n")
			for _, h := range out.MITM {
				sb.WriteString("    - \"" + h + "\"\n")
			}
			sb.WriteString("\n")
		}

		if len(out.HeaderRewrites) > 0 {
			sb.WriteString("  header-rewrite:\n")
			for _, r := range out.HeaderRewrites {
				sb.WriteString("    - " + r + "\n")
			}
			sb.WriteString("\n")
		}

		if len(out.URLRewrites) > 0 {
			sb.WriteString("  url-rewrite:\n")
			for _, r := range out.URLRewrites {
				sb.WriteString("    - " + r + "\n")
			}
			sb.WriteString("\n")
		}

		if len(out.MapLocal) > 0 {
			for _, r := range out.MapLocal {
				sb.WriteString("  # map-local: " + r + "\n")
			}
			sb.WriteString("\n")
		}

		if len(out.Scripts) > 0 {
			sb.WriteString("  script:\n")
			for _, s := range out.Scripts {
				sb.WriteString("    - >-\n      " + s + "\n")
			}
			sb.WriteString("\n")
		}
	}

	if len(out.Scripts) > 0 {
		sb.WriteString("script-providers:\n")
		seen := make(map[string]bool)
		for _, s := range out.Scripts {
			parts := strings.SplitN(s, "=", 2)
			name := strings.TrimSpace(parts[0])
			if !seen[name] {
				seen[name] = true
				spIdx := strings.Index(s, "script-path=")
				if spIdx >= 0 {
					spRest := s[spIdx+len("script-path="):]
					spEnd := strings.IndexByte(spRest, ',')
					spVal := spRest
					if spEnd >= 0 {
						spVal = spRest[:spEnd]
					}
					spVal = cleanRegexEscapes(spVal)
					sb.WriteString("  " + name + ":\n")
					sb.WriteString("    url: " + spVal + "\n")
					sb.WriteString("    interval: 86400\n")
				}
			}
		}
	}

	return sb.String()
}

// --- Generic fallback ---

func (p *Parser) convertToGenericFormat(modules []ParsedModule, target string, args map[string]string) string {
	var rewrites, rules, scripts, mitm []string
	var name, desc string

	for _, mod := range modules {
		name = mod.Name
		desc = mod.Desc
		mitm = append(mitm, mod.MITM...)
		rules = append(rules, mod.Rules...)
		for _, rw := range mod.Rewrites {
			rewrites = append(rewrites, fmt.Sprintf("%s -> %s", rw.Pattern, rw.Replacement))
		}
		for _, rw := range mod.Scripts {
			scripts = append(scripts, fmt.Sprintf("%s %s", rw.ScriptType, rw.ScriptPath))
		}
	}

	mitm = uniqueStrings(mitm)
	var sb strings.Builder
	if name != "" {
		sb.WriteString(fmt.Sprintf("#!name=%s\n", name))
	}
	if desc != "" {
		sb.WriteString(fmt.Sprintf("#!desc=%s\n", desc))
	}
	sb.WriteString("\n")
	if len(rules) > 0 {
		sb.WriteString("[Rule]\n")
		for _, r := range rules {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}
	if len(rewrites) > 0 {
		sb.WriteString("[URL Rewrite]\n")
		for _, r := range rewrites {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}
	if len(scripts) > 0 {
		sb.WriteString("[Script]\n")
		for _, s := range scripts {
			sb.WriteString(s + "\n")
		}
		sb.WriteString("\n")
	}
	if len(mitm) > 0 {
		sb.WriteString("[MITM]\n")
		sb.WriteString("hostname = %APPEND% " + strings.Join(mitm, ", ") + "\n")
	}
	return sb.String()
}

// --- Utility functions ---

// cleanRegexEscapes removes regex backslash escapes from a string
// so it can be used as a plain URL value. \. → ., \- → -, etc.
func cleanRegexEscapes(s string) string {
	s = strings.ReplaceAll(s, `\.`, ".")
	s = strings.ReplaceAll(s, `\-`, "-")
	s = strings.ReplaceAll(s, `\_`, "_")
	s = strings.ReplaceAll(s, `\+`, "+")
	s = strings.ReplaceAll(s, `\(`, "(")
	s = strings.ReplaceAll(s, `\)`, ")")
	s = strings.ReplaceAll(s, `\[`, "[")
	s = strings.ReplaceAll(s, `\]`, "]")
	s = strings.ReplaceAll(s, `\?`, "?")
	s = strings.ReplaceAll(s, `\*`, "*")
	s = strings.ReplaceAll(s, `\$`, "$")
	s = strings.ReplaceAll(s, `\^`, "^")
	s = strings.ReplaceAll(s, `\|`, "|")
	s = strings.ReplaceAll(s, `\{`, "{")
	s = strings.ReplaceAll(s, `\}`, "}")
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

// sanitizeName creates a valid script name from a URL pattern.
func sanitizeName(pattern string) string {
	s := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(pattern, "_")
	s = strings.Trim(s, "_")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "script"
	}
	return s
}

// parseSurgeSections parses content into sections like [Rule], [Script], etc.
func parseSurgeSections(content string) map[string][]string {
	sections := make(map[string][]string)
	var currentSection string
	lines := strings.Split(content, "\n")
	sectionRegex := regexp.MustCompile(`^\[(.+)\]`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if matches := sectionRegex.FindStringSubmatch(line); len(matches) > 1 {
			currentSection = matches[1]
			continue
		}
		if currentSection != "" {
			sections[currentSection] = append(sections[currentSection], line)
		}
	}
	return sections
}

// parseMITMSection parses the MITM hostname section.
func parseMITMSection(lines []string) []string {
	var hosts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "hostname") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				hostStr := strings.TrimSpace(parts[1])
				hostStr = strings.ReplaceAll(hostStr, "%APPEND%", "")
				hostStr = strings.ReplaceAll(hostStr, "%INSERT%", "")
				for _, h := range strings.Split(hostStr, ",") {
					h = strings.TrimSpace(h)
					if h != "" {
						hosts = append(hosts, h)
					}
				}
			}
		}
	}
	return hosts
}

// extractHostnames extracts MITM hostnames from URL patterns.
// Pattern examples: ^https?://api\.example\.com/path, ^https?://test\.com
// The hostname includes dots (possibly escaped as \.) up to the first / or regex metacharacter.
func extractHostnames(pattern string) []string {
	var hosts []string
	// Match :// followed by the hostname (allowing \. and alphanumeric/hyphen)
	re := regexp.MustCompile(`\\?://([a-zA-Z0-9_\\.-]+)`)
	matches := re.FindStringSubmatch(pattern)
	if len(matches) > 1 {
		host := matches[1]
	host = strings.ReplaceAll(host, `\.`, ".")
	host = strings.ReplaceAll(host, `\-`, "-")
	host = strings.ReplaceAll(host, `\_`, "_")
	host = strings.ReplaceAll(host, `\`, "")
	host = strings.TrimRight(host, ".-")
		if host != "" && !strings.Contains(host, "(") && !strings.Contains(host, "[") &&
			!strings.Contains(host, "*") && !strings.Contains(host, "?") && !strings.Contains(host, "+") {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// uniqueStrings removes duplicates from a string slice.
func uniqueStrings(s []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(s))
	for _, v := range s {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

func filterCommented(lines []string) []string {
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
		}
	}
	return result
}
