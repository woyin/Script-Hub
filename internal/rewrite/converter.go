// converter.go 是重写格式转换的调度入口与跨平台共享工具。
// 各目标平台的具体转换逻辑按平台拆分到独立文件：
//   - converter_surge.go  Surge / Shadowrocket / Egern / LanceX
//   - converter_loon.go   Loon
//   - converter_qx.go     Quantumult X
//   - converter_generic.go 未知 target 的兜底
//   - stash.go            Stash（复用 surgeOutput，但脚本与序列化为 YAML）
// surgeOutput 类型与共享工具函数（uniqueStrings / sanitizeName / 等）留在本文件，
// 供多个平台转换器复用。

package rewrite

import (
	"regexp"
	"strings"
)

const scriptHubRawBase = "https://raw.githubusercontent.com/Script-Hub-Org/Script-Hub/main"

type surgeOutput struct {
	URLRewrites        []string
	HeaderRewrites     []string
	Scripts            []string
	MapLocal           []string
	Rules              []string
	MITM               []string
	ForceHTTPHosts     []string
	Panels             []string
	Hosts              []string
	HostEntries        []HostInfo // raw host entries for target-specific routing
	// Stash-specific output buffers (populated only by classifyStashScript).
	// These hold pre-formatted YAML entries so formatStashOutput does not need
	// to re-parse Surge-style [Script] lines.
	StashScripts   []string // http-request/response YAML entries
	StashCron      []string // cron YAML entries
	StashTiles     []string // generic/tile YAML entries
	StashProviders []string // script-provider YAML entries
	stashNameIdx map[string]int  // per-name counter for _<num> suffixes (Stash)
	Name           string
	Desc               string
	Icon               string
	CategoryKey        string
	CategoryValue      string
	MetaExtra          []string
	SgArg              []SgArgument
	BodyRewrites       []BodyRewriteEntry
	ConditionalMITMKey string
	SkipProxy          []string
	RealIP             []string
	HNAddMethod        string // %APPEND% or %INSERT%
}

// convertModules converts parsed modules to the target app format.
func (p *Parser) convertModules(modules []ParsedModule, targetApp string, args map[string]string) string {
	target := strings.ToLower(targetApp)

	switch {
	// Egern / LanceX are Surge-compatible clients and share the Surge output path.
	case strings.Contains(target, "surge") || strings.Contains(target, "shadowrocket") ||
		strings.Contains(target, "egern") || strings.Contains(target, "lancex"):
		return p.convertToSurgeFormat(modules, target, args)
	case strings.Contains(target, "qx"):
		return p.convertToQXFormat(modules, target, args)
	case strings.Contains(target, "loon"):
		return p.convertToLoonFormat(modules, target, args)
	case strings.Contains(target, "stash"):
		return p.convertToStashFormat(modules, target, args)
	default:
		return p.convertToGenericFormat(modules, target, args)
	}
}

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

// Per-line / one-shot regexes hoisted to package level.
var (
	sanitizeNameRe  = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	surgeSectionRe  = regexp.MustCompile(`^\[(.+)\]`)
	extractHostRe   = regexp.MustCompile(`\\?://([a-zA-Z0-9_\\.-]+)`)
)

// sanitizeName creates a valid script name from a URL pattern.
func sanitizeName(pattern string) string {
	s := sanitizeNameRe.ReplaceAllString(pattern, "_")
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
// parseSurgeSections splits Surge/Loon module content into a section map keyed
// by the bracketed header (e.g. "Rule", "Script", "MITM"]). Lines outside any
// section are collected under the empty key "". Mirrors a lightweight subset of
// INI parsing tolerant of comments and blank lines.
func parseSurgeSections(content string) map[string][]string {
	sections := make(map[string][]string)
	var currentSection string
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if matches := surgeSectionRe.FindStringSubmatch(line); len(matches) > 1 {
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
	matches := extractHostRe.FindStringSubmatch(pattern)
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

// stripMatchingOuterQuotes removes one matched pair of surrounding quotes
// ("..." or '...') from value, mirroring Rewrite-Parser.js regex
// /^"(.+)"$/.replace -> $1 then /^'(.+)'$/.replace -> $1. Unlike strings.Trim,
// it only strips when the first and last characters form a matching pair, so
// internal quotes are preserved untouched.
// stripMatchingOuterQuotes removes a single matching pair of surrounding
// double or single quotes from value, if both ends are quoted with the same
// character. Used to normalize header-rewrite / mock values during parsing.
func stripMatchingOuterQuotes(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == '"' && last == '"' {
			return value[1 : len(value)-1]
		}
		if first == '\'' && last == '\'' {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// filterCommented returns lines with comment lines removed, except #! directive
// lines (e.g. #!arguments, #!error=404) which carry semantic meaning and must
// be preserved even when comment stripping (del=true) is requested.
func filterCommented(lines []string) []string {
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Keep #! lines (e.g. #!error=404); drop other comment lines when del=true
		if strings.HasPrefix(trimmed, "#!") {
			result = append(result, line)
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
		}
	}
	return result
}
