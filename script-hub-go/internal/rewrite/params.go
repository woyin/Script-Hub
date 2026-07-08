package rewrite

import (
	"fmt"
	"regexp"
	"strings"
)

// --- Parameter Modification Functions ---
// These implement the same parameter modification logic as the original
// Rewrite-Parser.js, applying keyword-based modifications to parsed entries.

// getArgArr splits a "+" separated argument string, replacing ➕ with +.
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

// ApplyArgModification modifies script arguments based on arg/argv parameters.
// arg=keyword1+keyword2, argv=value1+value2
// If a script's pattern or path contains the keyword, its argument is replaced.
func ApplyArgModification(scripts []ParsedRewrite, argTarget, argv string) []ParsedRewrite {
	targets := getArgArr(argTarget)
	values := getArgArr(argv)
	if len(targets) == 0 || len(values) == 0 {
		return scripts
	}
	// Must have matching counts
	count := len(targets)
	if len(values) < count {
		count = len(values)
	}

	for i := range scripts {
		for j := 0; j < count; j++ {
			if containsKeyword(scripts[i], targets[j]) {
				scripts[i].Arguments = values[j]
			}
		}
	}
	return scripts
}

// ApplyScriptNameModification modifies script names based on njsnametarget/njsname parameters.
func ApplyScriptNameModification(scripts []ParsedRewrite, nameTarget, newName string) []ParsedRewrite {
	targets := getArgArr(nameTarget)
	names := getArgArr(newName)
	if len(targets) == 0 || len(names) == 0 {
		return scripts
	}
	count := len(targets)
	if len(names) < count {
		count = len(names)
	}

	for i := range scripts {
		for j := 0; j < count; j++ {
			if containsKeyword(scripts[i], targets[j]) {
				scripts[i].Replacement = names[j]
			}
		}
	}
	return scripts
}

// ApplyTimeoutModification modifies script timeouts based on timeoutt/timeoutv parameters.
func ApplyTimeoutModification(scripts []ParsedRewrite, timeoutTarget, timeoutVal string) []ParsedRewrite {
	targets := getArgArr(timeoutTarget)
	values := getArgArr(timeoutVal)
	if len(targets) == 0 || len(values) == 0 {
		return scripts
	}
	count := len(targets)
	if len(values) < count {
		count = len(values)
	}

	for i := range scripts {
		for j := 0; j < count; j++ {
			if containsKeyword(scripts[i], targets[j]) {
				var t int
				fmt.Sscanf(values[j], "%d", &t)
				if t > 0 {
					scripts[i].Timeout = t
				}
			}
		}
	}
	return scripts
}

// ApplyEngineModification modifies script engine based on enginet/enginev parameters.
// Surge-specific: adds engine=VALUE to the script config.
func ApplyEngineModification(scripts []ParsedRewrite, engineTarget, engineVal string) []ParsedRewrite {
	targets := getArgArr(engineTarget)
	values := getArgArr(engineVal)
	if len(targets) == 0 || len(values) == 0 {
		return scripts
	}
	count := len(targets)
	if len(values) < count {
		count = len(values)
	}

	for i := range scripts {
		for j := 0; j < count; j++ {
			if containsKeyword(scripts[i], targets[j]) {
				// Append engine parameter to the script's Arguments or ScriptPath
				// In Surge, engine is specified as a script parameter
				engineSuffix := ",engine=" + values[j]
				if scripts[i].Arguments != "" {
					scripts[i].Arguments += engineSuffix
				} else {
					scripts[i].Arguments = "engine=" + values[j]
				}
			}
		}
	}
	return scripts
}

// ApplyCronModification modifies cron expressions based on cron/cronexp parameters.
// cron=keyword1+keyword2, cronexp=expression1+expression2 (dots become spaces)
func ApplyCronModification(scripts []ParsedRewrite, cronTarget, cronExp string) []ParsedRewrite {
	targets := getArgArr(cronTarget)
	exps := getArgArr(cronExp)
	if len(targets) == 0 || len(exps) == 0 {
		return scripts
	}
	count := len(targets)
	if len(exps) < count {
		count = len(exps)
	}

	for i := range scripts {
		if scripts[i].ScriptType != "cron" {
			continue
		}
		for j := 0; j < count; j++ {
			if containsKeyword(scripts[i], targets[j]) {
				// Replace dots with spaces in cron expression
				cronExpr := strings.ReplaceAll(exps[j], ".", " ")
				scripts[i].CronExp = cronExpr
			}
		}
	}
	return scripts
}

// ApplyPolicyToRules adds a policy to rules that don't have one.
func ApplyPolicyToRules(rules []string, policy string) []string {
	if policy == "" {
		return rules
	}

	for i, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" || strings.HasPrefix(rule, "#") {
			continue
		}
		parts := strings.SplitN(rule, ",", 3)
		if len(parts) >= 2 {
			// Check if there's already a policy (3rd part)
			hasPolicy := false
			if len(parts) >= 3 {
				third := strings.TrimSpace(parts[2])
				// If the third part doesn't contain only no-resolve/extended-matching/pre-matching, it has a policy
				thirdLower := strings.ToLower(third)
				if thirdLower != "" && thirdLower != "no-resolve" &&
					!strings.Contains(thirdLower, "extended-matching") &&
					!strings.Contains(thirdLower, "pre-matching") {
					hasPolicy = true
				}
			}
			if !hasPolicy {
				// Add policy
				if len(parts) == 2 {
					rules[i] = rule + "," + policy
				} else {
					// Has flags like no-resolve, append policy after them
					rules[i] = rule + "," + policy
				}
			}
		}
	}
	return rules
}

// ApplyMITMAdditions adds hostnames to the MITM list.
func ApplyMITMAdditions(mitm []string, hnadd string) []string {
	if hnadd == "" {
		return mitm
	}
	for _, h := range strings.Split(hnadd, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			mitm = append(mitm, h)
		}
	}
	return mitm
}

// ApplyMITMDeletions removes hostnames from the MITM list.
func ApplyMITMDeletions(mitm []string, hndel string) []string {
	if hndel == "" {
		return mitm
	}
	delMap := make(map[string]bool)
	for _, h := range strings.Split(hndel, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			delMap[h] = true
		}
	}

	var result []string
	for _, h := range mitm {
		if !delMap[strings.TrimSpace(h)] {
			result = append(result, h)
		}
	}
	return result
}

// ApplyMITNRegexDeletions removes hostnames matching a regex from the MITM list.
func ApplyMITNRegexDeletions(mitm []string, hnregdel string) []string {
	if hnregdel == "" {
		return mitm
	}
	re, err := regexp.Compile(hnregdel)
	if err != nil {
		return mitm
	}

	var result []string
	for _, h := range mitm {
		if !re.MatchString(h) {
			result = append(result, h)
		}
	}
	return result
}

// ApplySynMitm syncs MITM hostnames to force-http-engine-hosts.
// Returns additional hostnames that should be added to force-http-engine-hosts.
func ApplySynMitm(mitm []string, synMitm bool) []string {
	if !synMitm {
		return nil
	}
	// Return the same hostnames for force-http-engine-hosts
	return append([]string{}, mitm...)
}

// ApplyDelCommented removes commented rewrites from the output.
func ApplyDelCommented(lines []string, del bool) []string {
	if !del {
		return lines
	}
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") || trimmed == "" {
			result = append(result, line)
		}
		// Keep section headers
		if strings.HasPrefix(trimmed, "[") {
			result = append(result, line)
		}
	}
	return result
}

// ApplyJsDelivr converts GitHub URLs in script paths to jsDelivr CDN URLs.
func ApplyJsDelivr(scripts []ParsedRewrite, enabled bool) []ParsedRewrite {
	if !enabled {
		return scripts
	}
	for i := range scripts {
		scripts[i].ScriptPath = jsDelivrConvert(scripts[i].ScriptPath)
	}
	return scripts
}

// jsDelivrConvert converts a GitHub raw URL to jsDelivr CDN URL.
func jsDelivrConvert(urlStr string) string {
	if urlStr == "" {
		return urlStr
	}
	if strings.HasPrefix(urlStr, "https://cdn.jsdelivr.net/") {
		return urlStr
	}
	if strings.HasPrefix(urlStr, "https://raw.githubusercontent.com/") {
		parts := strings.SplitN(strings.TrimPrefix(urlStr, "https://raw.githubusercontent.com/"), "/", 4)
		if len(parts) >= 3 {
			user := parts[0]
			repo := parts[1]
			branch := parts[2]
			path := ""
			if len(parts) >= 4 {
				path = parts[3]
			}
			return fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s/%s@%s/%s", user, repo, branch, path)
		}
	}
	return urlStr
}

// ApplyKeepHeader determines whether to keep headers in Map Local/echo-response.
// When keepHeader is true, Map Local and echo-response entries should preserve
// their header/content-type information in the output.
// This affects how the converter generates output for these entry types.
func ApplyKeepHeader(args map[string]string) bool {
	return isTrue(args["keepHeader"])
}

// ApplyMetadataOverrides overrides name, desc, icon, category from parameters.
func ApplyMetadataOverrides(module *ParsedModule, args map[string]string) {
	if n, ok := args["n"]; ok && n != "" {
		// Format: name+desc or name desc (URL decoding turns + into space)
		// Use ➕ for literal +
		sep := "+"
		if !strings.Contains(n, "+") && strings.Contains(n, " ") {
			sep = " "
		}
		parts := strings.SplitN(n, sep, 2)
		if len(parts) >= 1 && parts[0] != "" {
			module.Name = strings.ReplaceAll(parts[0], "➕", "+")
		}
		if len(parts) >= 2 && parts[1] != "" {
			module.Desc = strings.ReplaceAll(parts[1], "➕", "+")
		}
	}
	if icon, ok := args["icon"]; ok && icon != "" {
		module.Icon = icon
	}
	// category parameter overrides parsed #!category (JS: modInfoObj.category = category)
	if cat, ok := args["category"]; ok && cat != "" {
		module.Category = cat
	}
}

// CategoryForOutput returns the category/keyword value mapped for the target app:
//   - Loon: category → emitted under "tag"
//   - others: keyword → emitted under "category"
// Mirrors Rewrite-Parser.js metadata key remapping.
func CategoryForOutput(module *ParsedModule, isLoon bool) (key, value string) {
	if isLoon {
		if module.Category != "" {
			return "tag", module.Category
		}
	} else {
		if module.Keyword != "" {
			return "category", module.Keyword
		}
		if module.Category != "" {
			return "category", module.Category
		}
	}
	return "", ""
}

// containsKeyword checks if a ParsedRewrite matches a keyword
// by looking at Pattern, ScriptPath, Replacement, and Arguments.
func containsKeyword(rw ParsedRewrite, keyword string) bool {
	return strings.Contains(rw.Pattern, keyword) ||
		strings.Contains(rw.ScriptPath, keyword) ||
		strings.Contains(rw.Replacement, keyword) ||
		strings.Contains(rw.Arguments, keyword)
}

// ApplySniPm appends extended-matching (sni) and pre-matching (pm) flags to
// rules whose value matches a keyword, mirroring JS sni/pm handling. IP-CIDR
// rules are skipped for sni (JS: x.search(/^ip6?-[ca]/i) == -1).
func ApplySniPm(rules []string, sni, pm string) []string {
	sniItems := getArgArr(sni)
	pmItems := getArgArr(pm)
	if len(sniItems) == 0 && len(pmItems) == 0 {
		return rules
	}
	for i, rule := range rules {
		trimmed := strings.TrimSpace(rule)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lower := strings.ToLower(trimmed)
		isIPRule := strings.HasPrefix(lower, "ip-cidr") || strings.HasPrefix(lower, "ip6-cidr") ||
			strings.HasPrefix(lower, "ip-asn") || strings.HasPrefix(lower, "geoip")
		appended := false
		if sniItems != nil && !isIPRule {
			for _, item := range sniItems {
				if strings.Contains(trimmed, item) {
					rules[i] = rule + ",extended-matching"
					appended = true
					break
				}
			}
		}
		if pmItems != nil {
			for _, item := range pmItems {
				if strings.Contains(trimmed, item) {
					if appended {
						rules[i] = rules[i] + ",pre-matching"
					} else {
						rules[i] = rule + ",pre-matching"
					}
					break
				}
			}
		}
	}
	return rules
}

// isTrue checks if a string represents a truthy value.
func isTrue(s string) bool {
	return s == "true" || s == "1" || s == "True"
}

// dedupRewrites removes duplicate rewrites by pattern (rwptn), matching JS rwBox.
// Body rewrite entries are intentionally NOT deduplicated (order-dependent).
func dedupRewrites(rws []ParsedRewrite) []ParsedRewrite {
	seen := make(map[string]bool)
	result := make([]ParsedRewrite, 0, len(rws))
	for _, rw := range rws {
		key := rw.Pattern
		if key == "" {
			result = append(result, rw)
			continue
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, rw)
		}
	}
	return result
}

// dedupScripts removes duplicate scripts by type+pattern+path+args+requiresBody,
// matching JS jsBox dedup keys.
func dedupScripts(rws []ParsedRewrite) []ParsedRewrite {
	seen := make(map[string]bool)
	result := make([]ParsedRewrite, 0, len(rws))
	for _, rw := range rws {
		key := strings.Join([]string{rw.ScriptType, rw.Pattern, rw.ScriptPath, rw.Arguments}, "|") +
			fmt.Sprintf("|%v", rw.RequiresBody)
		if !seen[key] {
			seen[key] = true
			result = append(result, rw)
		}
	}
	return result
}
