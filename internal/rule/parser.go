// Input: context, fmt, log, regexp, strings, internal/config, internal/httpclient, internal/types, internal/util
// Output: type ParseInput, type ParseOutput, type Parser, func NewParser(), func (Parser) Parse(), 规则解析与格式化函数
// Pos: 业务层-规则集转换引擎，解析各平台规则集并转换为目标平台格式
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package rule 实现规则集解析与转换引擎。
// 将各平台规则集格式解析为统一中间表示，再转换为目标平台格式。
// 对应 JS 版 rule-parser.js 的完整功能。
package rule

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/types"
	"github.com/script-hub-org/script-hub/internal/util"
)

// ParseInput contains the input parameters for rule parsing.
type ParseInput struct {
	URLs      []string
	TargetApp string
	Arguments map[string]string
}

// ParseOutput contains the parsed and converted rule output.
type ParseOutput struct {
	Content string
	Headers map[string]string
	Status  int
}

// GetResponse implements the server.ResponseWriter interface.
func (o ParseOutput) GetResponse() types.ResponseData {
	return types.ResponseData{
		Status:  o.Status,
		Headers: o.Headers,
		Body:    o.Content,
	}
}

// Parser handles rule set parsing and conversion.
type Parser struct {
	cfg    *config.Config
	client *httpclient.Client
}

// NewParser creates a new rule parser.
func NewParser(cfg *config.Config) *Parser {
	return &Parser{
		cfg:    cfg,
		client: httpclient.NewClient(cfg.HTTPTimeout),
	}
}

// ruleLine represents a single parsed rule line.
type ruleLine struct {
	RuleType    string
	Value       string
	NoResolve   bool
	ExtMatch    string
	Policy      string
	Raw         string
	Excluded    bool
	Unsupported bool
}

// Parse fetches remote rule content and converts it to the target format.
func (p *Parser) Parse(ctx context.Context, input ParseInput) (ParseOutput, error) {
	var body string
	localText := input.Arguments["localtext"]

	if len(input.URLs) > 0 {
		if input.URLs[0] == "http://local.text" || input.URLs[0] == "http://local.text/" {
			body = localText
		} else {
			var bodies []string
			reqHeaders := httpclient.ParseCustomHeaders(input.Arguments["headers"])
			for _, u := range input.URLs {
				decodedURL, err := decodeURL(u)
				if err != nil {
					decodedURL = u
				}
				content, status, err := p.client.GetWithHeaders(ctx, decodedURL, reqHeaders)
				if err != nil {
					log.Printf("rule fetch error for %s: %v", decodedURL, err)
					continue
				}
				if status == 404 {
					bodies = append(bodies, "#!error=404: Not Found")
				} else if status == 200 {
					bodies = append(bodies, content)
				}
			}
			if len(bodies) > 0 {
				body = strings.Join(bodies, "\n\n")
			}
			if localText != "" {
				if body != "" {
					body = body + "\n"
				}
				body = body + localText
			}
		}
	} else {
		body = localText
	}

	if body == "" {
		return ParseOutput{
			Content: "",
			Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Status:  200,
		}, nil
	}

	// Parse and convert rules
	rules := p.parseRules(body, input)

	// Convert to target format
	output := p.formatOutput(rules, input.TargetApp)

	// Restore commas protected in regex quantifiers {N,M} in the final output
	output = strings.ReplaceAll(output, "t&zd;", ",")
	// Collapse the JS-style excluded marker spacing that may survive in output
	output = strings.ReplaceAll(output, " ;#", " #")

	return ParseOutput{
		Content: output,
		Headers: map[string]string{
			"Content-Type":                 "text/plain; charset=utf-8",
			"Access-Control-Allow-Origin":  "*",
			"Access-Control-Allow-Methods": "POST,GET,OPTIONS,PUT,DELETE",
			"Access-Control-Allow-Headers": "Origin, X-Requested-With, Content-Type, Accept",
		},
		Status: 200,
	}, nil
}

// parseRules parses raw rule content into structured rule lines.
func (p *Parser) parseRules(content string, input ParseInput) []ruleLine {
	lines := strings.Split(content, "\n")
	var rules []ruleLine

	// Regex to strip comments
	commentRegex := regexp.MustCompile(`(^[^#].+)\x20+//.+`)
	// Regex to detect CIDR notation
	cidrRegex := regexp.MustCompile(`[0-9]/[0-9]`)
	cidr6Regex := regexp.MustCompile(`([0-9]|[a-fA-F]):([0-9]|[a-fA-F])`)
	// Regex to protect commas inside regex quantifiers like {1,2}
	commaGuardRegex := regexp.MustCompile(`(\{[0-9]+),([0-9]*\})`)
	// Regex to strip script/complex pattern lines (lines starting with non-U)
	scriptLineRegex := regexp.MustCompile(`^[^U].*(\[|=|{|\\|/.*\.js)`)

	ipNoResolve := util.IsTrue(input.Arguments["nore"])

	includeItems := util.GetArgArr(input.Arguments["y"])
	excludeItems := util.GetArgArr(input.Arguments["x"])

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)

		// Normalize line — mirrors JS preprocessing order
		line = regexp.MustCompile(`^payload:`).ReplaceAllString(line, "")
		line = regexp.MustCompile(`^ *(#|;|//)`).ReplaceAllString(line, "#")
		line = regexp.MustCompile(`^ *- *`).ReplaceAllString(line, "")
		line = commentRegex.ReplaceAllString(line, "$1")
		// Protect commas inside {N,M} quantifiers before any comma-based split
		line = commaGuardRegex.ReplaceAllString(line, "${1}t&zd;${2}")
		// Drop script/complex pattern lines (matches JS regex; only affects non-U lines)
		line = scriptLineRegex.ReplaceAllString(line, "")
		line = strings.ReplaceAll(line, "'", "")
		line = strings.ReplaceAll(line, `"`, "")

		// Convert shorthand prefixes: consume the prefix char plus an optional leading dot
		line = regexp.MustCompile(`^(\.|\*|\+)\.?`).ReplaceAllString(line, "DOMAIN-SUFFIX,")

		// Include (y): strip leading comment mark when keyword matches the whole line
		if includeItems != nil {
			for _, item := range includeItems {
				if strings.Contains(line, item) {
					line = strings.TrimPrefix(line, "#")
				}
			}
		}
		// Exclude (x): mark line as excluded with ;# prefix when keyword matches
		if excludeItems != nil {
			for _, item := range excludeItems {
				if strings.Contains(line, item) {
					line = ";#" + line
				}
			}
		}

		// ipNoResolve: append ,no-resolve only when nore=true and rule is ip-cidr/ip-cidr6
		if ipNoResolve {
			if regexp.MustCompile(`^ip6?-[ca]`).MatchString(strings.ToLower(line)) {
				line = line + ",no-resolve"
			}
		}

		// Now drop comment lines (after y may have uncommented them)
		line = regexp.MustCompile(`^#.+`).ReplaceAllString(line, "")

		// Skip empty lines and section headers
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}

		// Auto-detect rule type for lines without comma separator
		if !strings.Contains(line, ",") {
			if cidrRegex.MatchString(line) {
				line = "IP-CIDR," + line
			} else if cidr6Regex.MatchString(line) {
				line = "IP-CIDR6," + line
			} else {
				line = "DOMAIN," + line
			}
		}

		// Parse the rule
		rl := p.parseRuleLine(line, input)
		if rl != nil {
			rules = append(rules, *rl)
		}
	}

	return rules
}

// parseRuleLine parses a single rule line into a ruleLine struct.
func (p *Parser) parseRuleLine(line string, input ParseInput) *ruleLine {
	// Detect excluded lines (marked with ;# prefix during preprocessing)
	excluded := false
	if strings.HasPrefix(line, ";#") {
		excluded = true
		line = strings.TrimPrefix(line, ";#")
	}

	rl := &ruleLine{Raw: line, Excluded: excluded}

	// Logical rules (OR/AND/NOT) are emitted verbatim, no further parsing
	upperFirst := strings.ToUpper(line)
	if strings.HasPrefix(upperFirst, "OR") || strings.HasPrefix(upperFirst, "AND") || strings.HasPrefix(upperFirst, "NOT") {
		rl.RuleType = upperFirst
		rl.Value = strings.ReplaceAll(line, "t&zd;", ",")
		rl.Raw = rl.Value
		return rl
	}

	parts := strings.SplitN(line, ",", 3)
	if len(parts) < 2 {
		return nil
	}

	ruleType := strings.TrimSpace(parts[0])
	// Restore commas protected in regex quantifiers {N,M} after the split
	ruleValue := strings.ReplaceAll(strings.TrimSpace(parts[1]), "t&zd;", ",")
	// Third field (flags/policy) may also carry protected commas
	third := ""
	if len(parts) >= 3 {
		third = strings.ReplaceAll(strings.TrimSpace(parts[2]), "t&zd;", ",")
	}

	// Check for no-resolve flag carried in the line
	noResolve := false
	if len(parts) >= 3 {
		flags := strings.ToLower(third)
		if strings.Contains(flags, "no-resolve") {
			noResolve = true
		}
	}

	// Normalize rule type
	ruleType = normalizeRuleType(ruleType)

	rl.RuleType = ruleType
	rl.Value = ruleValue
	rl.NoResolve = noResolve

	// SNI sniffing: matches the whole line, skip ip-cidr/ip-cidr6 rules
	sni := input.Arguments["sni"]
	if sni != "" {
		isIPRule := regexp.MustCompile(`^ip6?-[ca]`).MatchString(strings.ToLower(line))
		if !isIPRule {
			for _, item := range util.GetArgArr(sni) {
				if strings.Contains(line, item) {
					rl.ExtMatch = ",extended-matching"
				}
			}
		}
	}

	pm := input.Arguments["pm"]
	if pm != "" {
		for _, item := range util.GetArgArr(pm) {
			if strings.Contains(line, item) {
				if rl.ExtMatch != "" {
					rl.ExtMatch += ",pre-matching"
				} else {
					rl.ExtMatch = ",pre-matching"
				}
			}
		}
	}

	policy := input.Arguments["policy"]
	if policy != "" {
		hasPolicy := false
		if len(parts) >= 3 {
			thirdLower := strings.ToLower(third)
			if thirdLower != "" && thirdLower != "no-resolve" &&
				!strings.Contains(thirdLower, "extended-matching") &&
				!strings.Contains(thirdLower, "pre-matching") {
				hasPolicy = true
			}
		}
		if !hasPolicy {
			rl.Policy = policy
		}
	}

	return rl
}

// formatOutput formats the parsed rules for the target platform.
func (p *Parser) formatOutput(rules []ruleLine, targetApp string) string {
	var ruleSet []string
	var otherRules []string
	var excludedRules []string

	target := strings.ToLower(targetApp)
	isStash := strings.Contains(target, "stash")
	isLoon := strings.Contains(target, "loon")
	// Egern / LanceX are Surge-compatible clients and share the Surge rule format.
	isSurge := strings.Contains(target, "surge") || strings.Contains(target, "shadowrocket") ||
		strings.Contains(target, "egern") || strings.Contains(target, "lancex")
	isShadowrocket := strings.Contains(target, "shadowrocket")
	isDomainSet := strings.Contains(target, "domain-set")
	isDomainSet2 := strings.HasSuffix(target, "2")

	for _, rl := range rules {
		// Excluded lines: emit the original line verbatim (matches JS outRules behavior),
		// normalizing HO-ST prefixes back to HOST for display.
		if rl.Excluded {
			excludedRules = append(excludedRules, hoStToHost(rl.Raw))
			continue
		}

		// Logical rules (OR/AND/NOT) and unsupported types are platform-dependent
		rt := strings.ToUpper(rl.RuleType)
		isLogical := rt == "OR" || rt == "AND" || rt == "NOT"
		if isLogical {
			if isStash || isLoon {
				// Stash/Loon do not support logical rules → other
				otherRules = append(otherRules, hoStToHost(rl.Value))
				continue
			}
			// Surge/Shadowrocket: emit verbatim
			ruleSet = append(ruleSet, rl.Value)
			continue
		}

		// Per-target unsupported type detection (matches JS regex sets)
		if isUnsupportedForTarget(rt, isStash, isLoon, isSurge) {
			otherRules = append(otherRules, hoStToHost(rl.Raw))
			continue
		}

		var formatted string
		switch {
		case isStash && !isDomainSet:
			formatted = formatStashRule(rl)
		case isLoon && !isDomainSet:
			formatted = formatLoonRule(rl)
		case isSurge && !isDomainSet:
			formatted = formatSurgeRule(rl, isShadowrocket)
		default:
			formatted = formatSurgeRule(rl, isShadowrocket)
		}
		if formatted != "" {
			ruleSet = append(ruleSet, formatted)
		}
	}

	ruleNum := len(ruleSet)
	otherNum := len(otherRules)
	excludedNum := len(excludedRules)

	var otherStr, excludedStr string
	if len(otherRules) > 0 {
		otherStr = "\n#不支持的规则:\n#" + strings.Join(otherRules, "\n#")
	}
	if len(excludedRules) > 0 {
		excludedStr = "\n#已排除规则:\n#" + strings.Join(excludedRules, "\n#")
	}

	// Handle domain-set format
	if isDomainSet && !isDomainSet2 {
		return p.formatDomainSet(ruleSet, ruleNum, otherNum, excludedNum, otherStr, excludedStr, isStash)
	}
	if isDomainSet2 {
		return p.formatDomainSet2(ruleSet, ruleNum, otherNum, excludedNum, otherStr, excludedStr, isStash)
	}

	// Standard rule-set format
	header := fmt.Sprintf("#规则数量:%d\n#不支持的规则数量:%d\n#已排除的规则数量:%d%s%s\n\n#-----------------以下为解析后的规则-----------------#\n\n",
		ruleNum, otherNum, excludedNum, otherStr, excludedStr)

	if isStash {
		return header + "payload:\n" + strings.Join(ruleSet, "\n")
	}
	return header + strings.Join(ruleSet, "\n")
}

// formatDomainSet formats rules as a domain set (only DOMAIN and DOMAIN-SUFFIX rules).
func (p *Parser) formatDomainSet(ruleSet []string, totalNum, otherNum, excludedNum int, otherStr, excludedStr string, isStash bool) string {
	var domainRules []string
	var nonDomainRules []string

	for _, r := range ruleSet {
		upperR := strings.ToUpper(r)
		if strings.Contains(upperR, "DOMAIN,") || strings.Contains(upperR, "DOMAIN-SUFFIX,") {
			domainRules = append(domainRules, r)
		} else {
			nonDomainRules = append(nonDomainRules, r)
		}
	}

	domainNum := len(domainRules)

	var result string
	if len(domainRules) > 0 {
		joined := strings.Join(domainRules, "\n")
		if !isStash {
			re1 := regexp.MustCompile(`(?im)^DOMAIN,`)
			re2 := regexp.MustCompile(`(?im)^DOMAIN-SUFFIX,`)
			joined = re1.ReplaceAllString(joined, "")
			joined = re2.ReplaceAllString(joined, ".")
		} else {
			re1 := regexp.MustCompile(`(?im)^  - DOMAIN,`)
			re2 := regexp.MustCompile(`(?im)^  - DOMAIN-SUFFIX,`)
			joined = re1.ReplaceAllString(joined, "")
			joined = re2.ReplaceAllString(joined, ".")
			// Strip policy part
			re3 := regexp.MustCompile(`^([^,]*),?.*`)
			lines := strings.Split(joined, "\n")
			for i, line := range lines {
				lines[i] = re3.ReplaceAllString(line, "$1")
			}
			joined = strings.Join(lines, "\n")
		}
		result = fmt.Sprintf("#总规则数量:%d\n#域名规则数量:%d\n#不支持的规则数量:%d\n#已排除的规则数量:%d%s%s\n\n#-----------------以下为解析后的规则-----------------#\n\n%s",
			totalNum, domainNum, otherNum, excludedNum, otherStr, excludedStr, joined)
	}
	return result
}

// formatDomainSet2 formats rules as domain set 2 (non-domain rules only).
func (p *Parser) formatDomainSet2(ruleSet []string, totalNum, otherNum, excludedNum int, otherStr, excludedStr string, isStash bool) string {
	var domainRules []string
	var nonDomainRules []string

	for _, r := range ruleSet {
		upperR := strings.ToUpper(r)
		if strings.Contains(upperR, "DOMAIN,") || strings.Contains(upperR, "DOMAIN-SUFFIX,") {
			domainRules = append(domainRules, r)
		} else {
			nonDomainRules = append(nonDomainRules, r)
		}
	}

	nonDomainNum := len(nonDomainRules)
	if len(nonDomainRules) == 0 {
		return ""
	}

	prefix := ""
	if isStash {
		prefix = "payload:\n"
	}

	return fmt.Sprintf("#总规则数量:%d\n#非域名规则数量:%d\n#不支持的规则数量:%d\n#已排除的规则数量:%d%s%s\n\n#-----------------以下为解析后的规则-----------------#\n\n%s%s",
		totalNum, nonDomainNum, otherNum, excludedNum, otherStr, excludedStr, prefix, strings.Join(nonDomainRules, "\n"))
}

// formatRuleLine formats a rule line for generic/surge output.
func formatRuleLine(rl ruleLine, target string) string {
	noResolve := ""
	if rl.NoResolve {
		noResolve = ",no-resolve"
	}
	extMatch := rl.ExtMatch
	policy := ""
	if rl.Policy != "" {
		policy = "," + rl.Policy
	}
	return fmt.Sprintf("%s,%s%s%s%s", rl.RuleType, rl.Value, noResolve, extMatch, policy)
}

// formatSurgeRule formats a rule for Surge/Shadowrocket.
func formatSurgeRule(rl ruleLine, isShadowrocket bool) string {
	ruleType := strings.ToUpper(rl.RuleType)
	ruleType = strings.ReplaceAll(ruleType, "PROCESS-PATH", "PROCESS-NAME")
	// Only Surge iOS uses DEST-PORT; Shadowrocket keeps DST-PORT
	if !isShadowrocket {
		ruleType = strings.ReplaceAll(ruleType, "DST-PORT", "DEST-PORT")
	}

	noResolve := ""
	if rl.NoResolve {
		noResolve = ",no-resolve"
	}
	extMatch := rl.ExtMatch
	policy := ""
	if rl.Policy != "" {
		policy = "," + rl.Policy
	}
	return fmt.Sprintf("%s,%s%s%s%s", ruleType, rl.Value, noResolve, extMatch, policy)
}

// formatLoonRule formats a rule for Loon.
func formatLoonRule(rl ruleLine) string {
	ruleType := strings.ToUpper(rl.RuleType)
	unsupported := map[string]bool{
		"DEST-PORT":    true,
		"PROTOCOL":     true,
		"PROCESS-NAME": true,
		"OR":           true,
		"AND":          true,
		"NOT":          true,
	}
	if unsupported[ruleType] {
		return ""
	}

	noResolve := ""
	if rl.NoResolve {
		noResolve = ",no-resolve"
	}
	policy := ""
	if rl.Policy != "" {
		policy = "," + rl.Policy
	}
	return fmt.Sprintf("%s,%s%s%s", ruleType, rl.Value, noResolve, policy)
}

// formatStashRule formats a rule for Stash.
func formatStashRule(rl ruleLine) string {
	ruleType := strings.ToUpper(rl.RuleType)

	if strings.HasPrefix(ruleType, "PROCESS") {
		if strings.Contains(rl.Value, "/") {
			ruleType = "PROCESS-PATH"
		} else {
			ruleType = "PROCESS-NAME"
		}
	}

	noResolve := ""
	if rl.NoResolve {
		noResolve = ",no-resolve"
	}
	policy := ""
	if rl.Policy != "" {
		policy = "," + rl.Policy
	}
	return fmt.Sprintf("  - %s,%s%s%s", ruleType, rl.Value, noResolve, policy)
}

// normalizeRuleType normalizes rule type names across platforms.
func normalizeRuleType(ruleType string) string {
	rt := strings.ToUpper(strings.TrimSpace(ruleType))
	replacements := map[string]string{
		"IP6-CIDR":      "IP-CIDR6",
		"DEST-PORT":     "DST-PORT",
		"HOST-WILDCARD": "HO-ST-WILDCARD",
	}
	if repl, ok := replacements[rt]; ok {
		return repl
	}
	// HOST → DOMAIN
	if rt == "HOST" {
		return "DOMAIN"
	}
	return rt
}

// isUnsupportedForTarget reports whether a rule type is unsupported on the
// target platform, mirroring the JS per-target regex sets.
func isUnsupportedForTarget(rt string, isStash, isLoon, isSurge bool) bool {
	if isStash {
		// ^(HO-ST|U|PROTOCOL|OR|AND|NOT)
		return strings.HasPrefix(rt, "HO-ST") || strings.HasPrefix(rt, "U") ||
			rt == "PROTOCOL" || rt == "OR" || rt == "AND" || rt == "NOT"
	}
	if isLoon {
		// ^(HO-ST|DST-PORT|PROTOCOL|PROCESS-NAME|OR|AND|NOT)
		return strings.HasPrefix(rt, "HO-ST") || rt == "DST-PORT" ||
			rt == "PROTOCOL" || strings.HasPrefix(rt, "PROCESS-NAME") ||
			rt == "OR" || rt == "AND" || rt == "NOT"
	}
	// Surge / Shadowrocket: ^(HO-ST)
	return strings.HasPrefix(rt, "HO-ST")
}

// hoStToHost restores HO-ST prefixes (from HOST-WILDCARD normalization) back
// to HOST for display in "other"/excluded output, matching JS behavior.
func hoStToHost(s string) string {
	return regexp.MustCompile(`(?i)^HO-ST`).ReplaceAllString(s, "HOST")
}

// Helper functions

func decodeURL(s string) (string, error) {
	// Simple URL decode - the URLs in the request are already mostly decoded
	return strings.ReplaceAll(s, " ", "%20"), nil
}
