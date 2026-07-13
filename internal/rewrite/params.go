package rewrite

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"sync"

	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/util"
)

// --- Parameter Modification Functions ---
// These implement the same parameter modification logic as the original
// Rewrite-Parser.js, applying keyword-based modifications to parsed entries.

// ApplyArgModification modifies script arguments based on arg/argv parameters.
// arg=keyword1+keyword2, argv=value1+value2
// If a script's pattern or path contains the keyword, its argument is replaced.
func ApplyArgModification(scripts []ParsedRewrite, argTarget, argv string) []ParsedRewrite {
	targets := util.GetArgArr(argTarget)
	values := util.GetArgArr(argv)
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
	targets := util.GetArgArr(nameTarget)
	names := util.GetArgArr(newName)
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
	targets := util.GetArgArr(timeoutTarget)
	values := util.GetArgArr(timeoutVal)
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
	targets := util.GetArgArr(engineTarget)
	values := util.GetArgArr(engineVal)
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
				scripts[i].Engine = values[j]
			}
		}
	}
	return scripts
}

// ApplyCronModification modifies cron expressions based on cron/cronexp parameters.
// cron=keyword1+keyword2, cronexp=expression1+expression2 (dots become spaces)
func ApplyCronModification(scripts []ParsedRewrite, cronTarget, cronExp string) []ParsedRewrite {
	targets := util.GetArgArr(cronTarget)
	exps := util.GetArgArr(cronExp)
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
//
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
// For AND/OR/NOT logical rules, ModifyRule is used to recursively apply flags.
func ApplySniPm(rules []string, sni, pm string) []string {
	sniItems := util.GetArgArr(sni)
	pmItems := util.GetArgArr(pm)
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

		upperTrimmed := strings.ToUpper(trimmed)
		isLogicalRule := strings.HasPrefix(upperTrimmed, "AND,") || strings.HasPrefix(upperTrimmed, "OR,") || strings.HasPrefix(upperTrimmed, "NOT,")

		if isLogicalRule {
			var flags RuleFlags
			if sniItems != nil {
				for _, item := range sniItems {
					if strings.Contains(trimmed, item) {
						flags.ExtendedMatching = true
						break
					}
				}
			}
			if pmItems != nil {
				for _, item := range pmItems {
					if strings.Contains(trimmed, item) {
						flags.PreMatching = true
						break
					}
				}
			}
			if flags.ExtendedMatching || flags.PreMatching {
				if result := ModifyRule(trimmed, "surge", flags); result != "" {
					rules[i] = result
				}
			}
			continue
		}

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

// keLeeIconURL is the icon name→URL mapping source (luestr/IconResource).
const keLeeIconURL = "https://raw.githubusercontent.com/luestr/IconResource/main/KeLee_icon.json"

var (
	keLeeIconCache []keLeeIcon
	keLeeIconMu    sync.Mutex
)

type keLeeIcon struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// keLeeIcons fetches (and caches) the KeLee icon list.
func keLeeIcons(ctx context.Context, client *httpclient.Client) []keLeeIcon {
	if client == nil {
		return nil
	}
	keLeeIconMu.Lock()
	defer keLeeIconMu.Unlock()
	if keLeeIconCache != nil {
		return keLeeIconCache
	}
	body, status, err := client.Get(ctx, keLeeIconURL)
	if err != nil || status != 200 {
		return nil
	}
	var wrapper struct {
		Icons []keLeeIcon `json:"icons"`
	}
	if json.Unmarshal([]byte(body), &wrapper) == nil {
		keLeeIconCache = wrapper.Icons
	}
	return keLeeIconCache
}

// lookupIconURL resolves a bare icon name to a URL via the KeLee mapping.
func lookupIconURL(ctx context.Context, client *httpclient.Client, name string) string {
	for _, ic := range keLeeIcons(ctx, client) {
		if ic.Name == name {
			return ic.URL
		}
	}
	return ""
}

// randomIconURL builds a random sticker icon URL from an icon library spec
// like "Doraemon(100P)". Mirrors Rewrite-Parser.js randomicon.
func randomIconURL(library string) string {
	name := library
	format := ".png"
	if i := strings.Index(library, "("); i > 0 {
		name = library[:i]
		rest := library[i+1:]
		if j := strings.Index(rest, "P"); j > 0 {
			rest = rest[:j]
		}
		if n, err := fmt.Sscanf(rest, "%d"); err == nil && n > 0 {
			_ = n
		}
	}
	if matched, _ := regexp.MatchString(`(?i)gif`, name); matched {
		format = ".gif"
	}
	// Parse count for random range (stickerStartNum=1001)
	count := 100
	if i := strings.Index(library, "("); i > 0 {
		rest := library[i+1:]
		if j := strings.Index(rest, "P"); j > 0 {
			fmt.Sscanf(rest[:j], "%d", &count)
		}
	}
	num := 1001 + rand.Intn(count)
	return "https://github.com/Toperlock/Quantumult/raw/main/icon/" + name + "/" + name + "-" + fmt.Sprintf("%d", num) + format
}

// ApplyIconReplacement resolves the module icon per Rewrite-Parser.js:
//   - if iconReplace is enabled (not "禁用"), use a random sticker from iconLibrary
//   - else if the icon is a bare name (no "/"), resolve via KeLee mapping
//
// Called from the parser where an httpclient is available.
func ApplyIconReplacement(ctx context.Context, module *ParsedModule, args map[string]string, client *httpclient.Client, isStashOrLoon bool) {
	iconReplace := argsValue(args, "iconReplace", "禁用")
	if isStashOrLoon && iconReplace != "禁用" {
		library := argsValue(args, "iconLibrary", "Doraemon(100P)")
		module.Icon = randomIconURL(library)
		return
	}
	icon := args["icon"]
	if icon == "" {
		icon = module.Icon
	}
	if icon != "" && !strings.Contains(icon, "/") {
		if u := lookupIconURL(ctx, client, icon); u != "" {
			module.Icon = u
		}
	}
}

func argsValue(args map[string]string, key, fallback string) string {
	if v, ok := args[key]; ok && v != "" {
		return v
	}
	return fallback
}

// LeadingTemplate holds the result of TakeLeadingTemplate.
type LeadingTemplate struct {
	Key  string // the template key (content inside {{{...}}})
	Rest string // the remaining text after removing the template
}

// TakeLeadingTemplate extracts a leading {{{key}}} template from a line,
// mirroring JS takeLeadingTemplate:
//
//	"{{{toggle_key}}} script-path=..." → {Key: "toggle_key", Rest: " script-path=..."}
//
// Returns nil if no template is found.
func TakeLeadingTemplate(str string) *LeadingTemplate {
	re := regexp.MustCompile(`^(\s*)\{\{\{([^{}]+)\}\}\}\s*(.*)$`)
	m := re.FindStringSubmatch(str)
	if m == nil {
		return nil
	}
	return &LeadingTemplate{
		Key:  strings.TrimSpace(m[2]),
		Rest: m[1] + m[3],
	}
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
