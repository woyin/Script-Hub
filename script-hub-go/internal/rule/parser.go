package rule

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/types"
)

// ParseInput contains the input parameters for rule parsing.
type ParseInput struct {
	URLs      []string
	TargetApp string
	Arguments map[string]string
	Headers   http.Header
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
	RuleType string
	Value    string
	NoResolve bool
	ExtMatch  string
	Raw      string
	Excluded bool
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
			for _, u := range input.URLs {
				decodedURL, err := decodeURL(u)
				if err != nil {
					decodedURL = u
				}
				content, status, err := p.client.Get(ctx, decodedURL)
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

	// Apply include/exclude filters
	rules = p.applyFilters(rules, input.Arguments)

	// Convert to target format
	output := p.formatOutput(rules, input.TargetApp)

	return ParseOutput{
		Content: output,
		Headers: map[string]string{
			"Content-Type":                "text/plain; charset=utf-8",
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

	ipNoResolve := isTrue(input.Arguments["nore"])
	_ = ipNoResolve

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)

		// Normalize line
		line = regexp.MustCompile(`^payload:`).ReplaceAllString(line, "")
		line = regexp.MustCompile(`^ *(#|;|//)`).ReplaceAllString(line, "#")
		line = regexp.MustCompile(`^ *- *`).ReplaceAllString(line, "")
		line = commentRegex.ReplaceAllString(line, "$1")
		line = strings.ReplaceAll(line, "'", "")
		line = strings.ReplaceAll(line, `"`, "")

		// Convert shorthand prefixes
		if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "+") {
			if len(line) > 1 && line[1] == '.' {
				line = "DOMAIN-SUFFIX," + line[2:]
			} else {
				line = "DOMAIN-SUFFIX," + line
			}
		}

		// Skip empty lines and section headers
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}

		// Skip comment lines
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Skip lines with scripts or complex patterns
		if matched, _ := regexp.MatchString(`^[^U].*(\[|=|{|\\|/.*\.js)`, line); matched && !strings.Contains(line, ",") {
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
		rl := p.parseRuleLine(line, ipNoResolve, input)
		if rl != nil {
			rules = append(rules, *rl)
		}
	}

	return rules
}

// parseRuleLine parses a single rule line into a ruleLine struct.
func (p *Parser) parseRuleLine(line string, ipNoResolve bool, input ParseInput) *ruleLine {
	rl := &ruleLine{Raw: line}

	parts := strings.SplitN(line, ",", 3)
	if len(parts) < 2 {
		return nil
	}

	ruleType := strings.TrimSpace(parts[0])
	ruleValue := strings.TrimSpace(parts[1])

	// Check for no-resolve flag
	noResolve := false
	if len(parts) >= 3 {
		flags := strings.ToLower(parts[2])
		if strings.Contains(flags, "no-resolve") {
			noResolve = true
		}
	}

	// Apply IP no-resolve setting — enabled by default for all IP-related rule types
	ipRulePrefixes := []string{
		"IP-CIDR", "IP-CIDR6", "IP-ASN", "GEOIP", "IP-SUFFIX",
		"SRC-GEOIP", "SRC-IP-ASN", "SRC-IP-CIDR", "SRC-IP-SUFFIX",
	}
	ruleTypeUpper := strings.ToUpper(ruleType)
	for _, prefix := range ipRulePrefixes {
		if strings.HasPrefix(ruleTypeUpper, prefix) {
			noResolve = true
			break
		}
	}

	// Normalize rule type
	ruleType = normalizeRuleType(ruleType)

	rl.RuleType = ruleType
	rl.Value = ruleValue
	rl.NoResolve = noResolve

	// Check for unsupported types for certain platforms
	unsupportedTypes := map[string]bool{
		"HOST-WILDCARD": true,
		"HOST":          true,
	}
	if unsupportedTypes[strings.ToUpper(ruleType)] {
		rl.Unsupported = true
	}

	// Check for SNI sniffing
	sni := input.Arguments["sni"]
	if sni != "" {
		sniItems := strings.Split(sni, "+")
		for _, item := range sniItems {
			if strings.Contains(ruleValue, item) && !strings.HasPrefix(strings.ToUpper(ruleType), "IP-CIDR") {
				rl.ExtMatch = ",extended-matching"
			}
		}
	}

	return rl
}

// applyFilters applies include/exclude filters to rules.
func (p *Parser) applyFilters(rules []ruleLine, args map[string]string) []ruleLine {
	includeItems := getArgArr(args["y"])
	excludeItems := getArgArr(args["x"])

	for i := range rules {
		// Include (uncomment)
		if includeItems != nil {
			for _, item := range includeItems {
				if strings.Contains(rules[i].Value, item) {
					rules[i].Excluded = false
				}
			}
		}
		// Exclude (comment out)
		if excludeItems != nil {
			for _, item := range excludeItems {
				if strings.Contains(rules[i].Value, item) {
					rules[i].Excluded = true
				}
			}
		}
	}
	return rules
}

// formatOutput formats the parsed rules for the target platform.
func (p *Parser) formatOutput(rules []ruleLine, targetApp string) string {
	var ruleSet []string
	var otherRules []string
	var excludedRules []string

	target := strings.ToLower(targetApp)
	isStash := strings.Contains(target, "stash")
	isLoon := strings.Contains(target, "loon")
	isSurge := strings.Contains(target, "surge") || strings.Contains(target, "shadowrocket")
	isDomainSet := strings.Contains(target, "domain-set")
	isDomainSet2 := strings.HasSuffix(target, "2")

	for _, rl := range rules {
		if rl.Unsupported {
			otherRules = append(otherRules, rl.Raw)
			continue
		}
		if rl.Excluded {
			excludedRules = append(excludedRules, formatRuleLine(rl, "surge"))
			continue
		}

		var formatted string
		switch {
		case isStash && !isDomainSet:
			formatted = formatStashRule(rl)
		case isLoon && !isDomainSet:
			formatted = formatLoonRule(rl)
		case isSurge && !isDomainSet:
			formatted = formatSurgeRule(rl)
		default:
			formatted = formatSurgeRule(rl)
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
	return fmt.Sprintf("%s,%s%s%s", rl.RuleType, rl.Value, noResolve, extMatch)
}

// formatSurgeRule formats a rule for Surge/Shadowrocket.
func formatSurgeRule(rl ruleLine) string {
	ruleType := strings.ToUpper(rl.RuleType)
	// Surge uses PROCESS-NAME instead of PROCESS-PATH
	ruleType = strings.ReplaceAll(ruleType, "PROCESS-PATH", "PROCESS-NAME")
	// Surge uses DEST-PORT instead of DST-PORT
	ruleType = strings.ReplaceAll(ruleType, "DST-PORT", "DEST-PORT")

	noResolve := ""
	if rl.NoResolve {
		noResolve = ",no-resolve"
	}
	extMatch := rl.ExtMatch
	return fmt.Sprintf("%s,%s%s%s", ruleType, rl.Value, noResolve, extMatch)
}

// formatLoonRule formats a rule for Loon.
func formatLoonRule(rl ruleLine) string {
	ruleType := strings.ToUpper(rl.RuleType)
	// Loon doesn't support some types
	unsupported := map[string]bool{
		"DEST-PORT":   true,
		"PROTOCOL":    true,
		"PROCESS-NAME": true,
		"OR":          true,
		"AND":         true,
		"NOT":         true,
	}
	if unsupported[ruleType] {
		return ""
	}

	noResolve := ""
	if rl.NoResolve {
		noResolve = ",no-resolve"
	}
	return fmt.Sprintf("%s,%s%s", ruleType, rl.Value, noResolve)
}

// formatStashRule formats a rule for Stash.
func formatStashRule(rl ruleLine) string {
	ruleType := strings.ToUpper(rl.RuleType)

	// Determine process type
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
	return fmt.Sprintf("  - %s,%s%s", ruleType, rl.Value, noResolve)
}

// normalizeRuleType normalizes rule type names across platforms.
func normalizeRuleType(ruleType string) string {
	rt := strings.ToUpper(strings.TrimSpace(ruleType))
	replacements := map[string]string{
		"IP6-CIDR":    "IP-CIDR6",
		"DEST-PORT":   "DST-PORT",
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

// Helper functions

func isTrue(s string) bool {
	return s == "true" || s == "1" || s == "True"
}

func getArgArr(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "+")
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = strings.ReplaceAll(p, "➕", "+")
	}
	return result
}

func decodeURL(s string) (string, error) {
	// Simple URL decode - the URLs in the request are already mostly decoded
	return strings.ReplaceAll(s, " ", "%20"), nil
}
