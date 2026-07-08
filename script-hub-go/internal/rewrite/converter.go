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
	Panels          []string
	Hosts           []string
	Name            string
	Desc            string
	Icon            string
	CategoryKey     string
	CategoryValue   string
	MetaExtra       []string
	SgArg           []SgArgument
	BodyRewrites    []BodyRewriteEntry
	ConditionalMITMKey string
	SkipProxy     []string
	RealIP        []string
	HNAddMethod   string // %APPEND% or %INSERT%
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
		out.CategoryKey, out.CategoryValue = CategoryForOutput(&mod, false)
		out.MetaExtra = append(out.MetaExtra, mod.MetaExtra...)
		out.SgArg = append(out.SgArg, mod.SgArg...)
		out.BodyRewrites = append(out.BodyRewrites, mod.BodyRewrites...)
		out.ConditionalMITMKey = mod.ConditionalMITMKey
		out.SkipProxy = append(out.SkipProxy, mod.SkipProxy...)
		out.RealIP = append(out.RealIP, mod.RealIP...)
		if mod.HNAddMethod != "" {
			out.HNAddMethod = mod.HNAddMethod
		}
		out.MITM = append(out.MITM, mod.MITM...)
		out.Rules = append(out.Rules, mod.Rules...)

		for _, rw := range mod.Rewrites {
			p.classifySurgeRewrite(rw, &out, args)
		}
		for _, rw := range mod.Scripts {
			p.classifySurgeScript(rw, &out, args)
		}
		// Panels (Surge only)
		for _, panel := range mod.Panels {
			out.Panels = append(out.Panels, formatSurgePanel(panel))
		}
		// Hosts
		for _, host := range mod.Hosts {
			out.Hosts = append(out.Hosts, fmt.Sprintf("%s = %s", host.Domain, host.Value))
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

	return applyArgumentsTemplate(p.formatSurgeOutput(out), out.SgArg, "surge")
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
		// Surge also emits Map Local entries for dict/array/200/img/tinygif/video
		switch rw.Type {
		case RewriteTypeRejectDict:
			out.MapLocal = append(out.MapLocal,
				fmt.Sprintf(`%s data-type=text data="{}" status-code=200 header="Content-Type:application/json"`, rw.Pattern))
		case RewriteTypeRejectArray:
			out.MapLocal = append(out.MapLocal,
				fmt.Sprintf(`%s data-type=text data="[]" status-code=200`, rw.Pattern))
		case RewriteTypeReject200:
			out.MapLocal = append(out.MapLocal,
				fmt.Sprintf(`%s data-type=text data=" " status-code=200`, rw.Pattern))
		case RewriteTypeRejectImg, RewriteTypeRejectTinyGif, RewriteTypeRejectVideo:
			out.MapLocal = append(out.MapLocal,
				fmt.Sprintf(`%s data-type=tiny-gif status-code=200`, rw.Pattern))
		}

	case RewriteTypeURLRewrite:
		out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s %s", rw.Pattern, rw.Replacement))

	case RewriteTypeHeaderRewrite:
		// Native Surge [Header Rewrite] pass-through
		out.HeaderRewrites = append(out.HeaderRewrites, rw.Replacement)

	case RewriteTypeMapLocal:
		// Native Surge [Map Local] pass-through
		out.MapLocal = append(out.MapLocal, rw.Replacement)

	case RewriteTypeMock:
		// Surge [Map Local] from a parsed mock entry
		ml := fmt.Sprintf("%s data-type=%s", rw.Pattern, rw.MockType)
		if rw.MockData != "" {
			ml += fmt.Sprintf(` data="%s"`, rw.MockData)
		} else if rw.MockDataPath != "" {
			ml += fmt.Sprintf(` data-path="%s"`, rw.MockDataPath)
		}
		if rw.MockStatus != "" {
			ml += fmt.Sprintf(` status-code=%s`, rw.MockStatus)
		}
		if rw.MockHeader != "" {
			ml += fmt.Sprintf(` header="%s"`, rw.MockHeader)
		}
		if rw.MockBase64 {
			ml = fmt.Sprintf("%s data-type=base64", rw.Pattern)
			if rw.MockData != "" {
				ml += fmt.Sprintf(` data="%s"`, rw.MockData)
			}
		}
		out.MapLocal = append(out.MapLocal, ml)

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

// loonBodyRewrite converts a BodyRewriteEntry to a Loon [Rewrite] line,
// mirroring Rewrite-Parser.js type2 mapping.
func loonBodyRewrite(br BodyRewriteEntry) string {
	type2 := ""
	switch br.Type {
	case "http-request":
		type2 = "request-body-replace-regex"
	case "http-response":
		type2 = "response-body-replace-regex"
	case "http-request-jq":
		type2 = "request-body-json-jq"
	case "http-response-jq":
		type2 = "response-body-json-jq"
	}
	if type2 == "" {
		return ""
	}
	return fmt.Sprintf("%s %s %s", br.Regex, type2, br.Value)
}

// applyArgumentsTemplate rewrites {key} placeholders in the body per the
// Surge #!arguments template system:
//   - Surge/Shadowrocket: {key} → {{{key}}} (Surge toggle template)
//   - Stash: {key} → actual value
//   - Loon: only strip {{{ }}} wrappers
// {{{ and }}} are first normalized to { and } across all platforms.
func applyArgumentsTemplate(body string, sgArg []SgArgument, platform string) string {
	if len(sgArg) == 0 && platform != "loon" {
		return body
	}
	body = strings.ReplaceAll(body, "{{{", "{")
	body = strings.ReplaceAll(body, "}}}", "}")
	switch platform {
	case "stash":
		for _, a := range sgArg {
			val := strings.TrimSpace(strings.Split(a.Value, ",")[0])
			body = strings.ReplaceAll(body, "{"+a.Key+"}", val)
		}
	case "surge":
		for _, a := range sgArg {
			body = strings.ReplaceAll(body, "{"+a.Key+"}", "{{{"+a.Key+"}}}")
		}
	}
	// loon: just the {{{ }}} normalization above
	return body
}

// formatSurgePanel formats a PanelInfo as a Surge [Panel] entry.
func formatSurgePanel(p PanelInfo) string {
	parts := []string{}
	if p.Title != "" {
		parts = append(parts, "title="+p.Title)
	}
	if p.Style != "" {
		parts = append(parts, "style="+p.Style)
	}
	if p.Content != "" {
		parts = append(parts, "content="+p.Content)
	}
	if p.ScriptName != "" {
		parts = append(parts, "script-name="+p.ScriptName)
	}
	if p.UpdateTimer != "" {
		parts = append(parts, "update-interval="+p.UpdateTimer)
	}
	return p.Name + " = " + strings.Join(parts, ", ")
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

	scriptName := rw.Replacement
	if scriptName == "" {
		scriptName = sanitizeName(rw.Pattern)
	}
	if rw.Tag != "" {
		scriptName = rw.Tag
	}

	// Cron scripts use a different Surge output format
	if rw.ScriptType == "cron" {
		cronexp := rw.CronExp
		if cronexp == "" {
			cronexp = "0 0 * * *"
		}
		var parts []string
		parts = append(parts, "type=cron")
		parts = append(parts, fmt.Sprintf("cronexp=%s", cronexp))
		parts = append(parts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
		parts = append(parts, fmt.Sprintf("timeout=%d", timeout))
		if rw.Arguments != "" {
			parts = append(parts, fmt.Sprintf("argument=%s", rw.Arguments))
		}
		if rw.WakeSystem {
			parts = append(parts, "wake-system=true")
		}
		if rw.Engine != "" {
			parts = append(parts, fmt.Sprintf("engine=%s", rw.Engine))
		}
		if rw.ScriptUpdateInterval != "" {
			parts = append(parts, fmt.Sprintf("script-update-interval=%s", rw.ScriptUpdateInterval))
		}
		out.Scripts = append(out.Scripts, scriptName+" = "+strings.Join(parts, ", "))
		return
	}

	// Standard script format
	var parts []string
	parts = append(parts, fmt.Sprintf("type=%s", rw.ScriptType))
	if rw.Pattern != "" {
		parts = append(parts, fmt.Sprintf("pattern=%s", rw.Pattern))
	}
	parts = append(parts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
	parts = append(parts, fmt.Sprintf("requires-body=%d", requiresBody))
	if rw.MaxSize != "" {
		parts = append(parts, fmt.Sprintf("max-size=%s", rw.MaxSize))
	} else {
		parts = append(parts, "max-size=0")
	}
	if rw.ScriptUpdateInterval != "" {
		parts = append(parts, fmt.Sprintf("script-update-interval=%s", rw.ScriptUpdateInterval))
	} else {
		parts = append(parts, "script-update-interval=86400")
	}
	parts = append(parts, fmt.Sprintf("timeout=%d", timeout))
	if rw.Arguments != "" {
		parts = append(parts, fmt.Sprintf("argument=%s", rw.Arguments))
	}
	if rw.EventName != "" {
		parts = append(parts, fmt.Sprintf("event-name=%s", rw.EventName))
	}
	if rw.BinaryBody {
		parts = append(parts, "binary-body-mode=true")
	}
	if rw.WakeSystem {
		parts = append(parts, "wake-system=true")
	}
	if rw.Engine != "" {
		parts = append(parts, fmt.Sprintf("engine=%s", rw.Engine))
	}
	if rw.ImgURL != "" {
		parts = append(parts, fmt.Sprintf("img-url=%s", rw.ImgURL))
	}
	if rw.Enable {
		parts = append(parts, "enable=true")
	}

	out.Scripts = append(out.Scripts, scriptName+" = "+strings.Join(parts, ", "))
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
	if out.CategoryKey != "" && out.CategoryValue != "" {
		sb.WriteString(fmt.Sprintf("#!%s=%s\n", out.CategoryKey, out.CategoryValue))
	}
	for _, m := range out.MetaExtra {
		sb.WriteString(m + "\n")
	}
	// Surge #!arguments metadata: key:value,...
	if len(out.SgArg) > 0 {
		var parts []string
		for _, a := range out.SgArg {
			val := strings.TrimSpace(strings.Split(a.Value, ",")[0])
			parts = append(parts, a.Key+":"+val)
		}
		sb.WriteString(fmt.Sprintf("#!arguments=%s\n", strings.Join(parts, ",")))
	}
	sb.WriteString("\n")

	// [General] section for Surge/Shadowrocket (skip-proxy, always-real-ip)
	if len(out.SkipProxy) > 0 || len(out.RealIP) > 0 {
		sb.WriteString("[General]\n")
		if len(out.SkipProxy) > 0 {
			sb.WriteString("skip-proxy = " + strings.Join(out.SkipProxy, ", ") + "\n")
		}
		if len(out.RealIP) > 0 {
			sb.WriteString("always-real-ip = " + strings.Join(out.RealIP, ", ") + "\n")
		}
		sb.WriteString("\n")
	}

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

	if len(out.BodyRewrites) > 0 {
		sb.WriteString("[Body Rewrite]\n")
		for _, br := range out.BodyRewrites {
			sb.WriteString(fmt.Sprintf("%s %s %s\n", br.Type, br.Regex, br.Value))
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

	if len(out.Panels) > 0 {
		sb.WriteString("[Panel]\n")
		for _, p := range out.Panels {
			sb.WriteString(p + "\n")
		}
		sb.WriteString("\n")
	}

	// [Host] section: user-defined host mappings + force-http-engine-hosts
	if len(out.Hosts) > 0 || len(out.ForceHTTPHosts) > 0 {
		sb.WriteString("[Host]\n")
		for _, h := range out.Hosts {
			sb.WriteString(h + "\n")
		}
		if len(out.ForceHTTPHosts) > 0 {
			sb.WriteString("force-http-engine-hosts = " + strings.Join(out.ForceHTTPHosts, ", ") + "\n")
		}
		sb.WriteString("\n")
	}

	if len(out.MITM) > 0 {
		sb.WriteString("[MITM]\n")
		hnKey := "hostname"
		if out.ConditionalMITMKey != "" {
			hnKey = out.ConditionalMITMKey
		}
		addMethod := out.HNAddMethod
			if addMethod == "" {
				addMethod = "%APPEND%"
			}
			sb.WriteString(hnKey + " = " + addMethod + " " + strings.Join(out.MITM, ", ") + "\n")
	}

	return sb.String()
}

// --- Loon ---

func (p *Parser) convertToLoonFormat(modules []ParsedModule, target string, args map[string]string) string {
	var rules, rewrites, scripts, mitm []string
	var name, desc, icon string
	var catKey, catValue string
	var metaExtra []string
	var sgArg []SgArgument
	var bodyRewrites []BodyRewriteEntry
	var skipProxy, realIP []string
	delComments := isTrue(args["del"])

	for _, mod := range modules {
		name = mod.Name
		desc = mod.Desc
		icon = mod.Icon
		catKey, catValue = CategoryForOutput(&mod, true)
		metaExtra = append(metaExtra, mod.MetaExtra...)
		sgArg = append(sgArg, mod.SgArg...)
		bodyRewrites = append(bodyRewrites, mod.BodyRewrites...)
		skipProxy = append(skipProxy, mod.SkipProxy...)
		realIP = append(realIP, mod.RealIP...)
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
		// Body rewrite → Loon [Rewrite] entries
		for _, br := range mod.BodyRewrites {
			if rw := loonBodyRewrite(br); rw != "" {
				rewrites = append(rewrites, rw)
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
	if catKey != "" && catValue != "" {
		sb.WriteString(fmt.Sprintf("#!%s=%s\n", catKey, catValue))
	}
	for _, m := range metaExtra {
		sb.WriteString(m + "\n")
	}
	sb.WriteString("\n")

	// [Argument] section: Surge #!arguments → Loon interactive parameters
	if len(sgArg) > 0 {
		sb.WriteString("[Argument]\n")
		for _, a := range sgArg {
			val := a.Value
			if a.Type == "switch" {
				if strings.HasPrefix(val, "true") {
					val = `"true","false"`
				} else {
					val = `"false","true"`
				}
			}
			tag := a.Tag
			if tag == "" {
				tag = "tag=" + a.Key + ", desc=" + a.Key
			}
			sb.WriteString(fmt.Sprintf("%s=%s,%s,%s\n", a.Key, a.Type, val, tag))
		}
		sb.WriteString("\n")
	}

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
	// [General] section (Loon: force-http-engine-hosts, skip-proxy, real-ip)
	synMitm := isTrue(args["synMitm"])
	if synMitm {
		skipProxy = uniqueStrings(skipProxy)
		realIP = uniqueStrings(realIP)
	}
	var generalItems []string
	if synMitm && len(mitm) > 0 {
		generalItems = append(generalItems, "force-http-engine-hosts = "+strings.Join(mitm, ", "))
	}
	if len(skipProxy) > 0 {
		generalItems = append(generalItems, "skip-proxy = "+strings.Join(skipProxy, ", "))
	}
	if len(realIP) > 0 {
		generalItems = append(generalItems, "real-ip = "+strings.Join(realIP, ", "))
	}
	if len(generalItems) > 0 {
		sb.WriteString("[General]\n")
		sb.WriteString(strings.Join(generalItems, "\n\n") + "\n\n")
	}

	if len(mitm) > 0 {
		sb.WriteString("[MITM]\n")
		sb.WriteString("hostname = " + strings.Join(mitm, ", ") + "\n")
	}
	return applyArgumentsTemplate(sb.String(), sgArg, "loon")
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

	case RewriteTypeMock, RewriteTypeMockRequestBody:
		// Loon mock-response-body / mock-request-body
		mockBodyType := "mock-response-body"
		if rw.Type == RewriteTypeMockRequestBody {
			mockBodyType = "mock-request-body"
		}
		ml := fmt.Sprintf("%s %s", rw.Pattern, mockBodyType)
		if rw.MockType != "" {
			ml += fmt.Sprintf(" data-type=%s", rw.MockType)
		}
		if rw.MockData != "" {
			ml += fmt.Sprintf(` data="%s"`, rw.MockData)
		} else if rw.MockDataPath != "" {
			ml += fmt.Sprintf(` data-path="%s"`, rw.MockDataPath)
		}
		if rw.MockStatus != "" {
			ml += fmt.Sprintf(" status-code=%s", rw.MockStatus)
		}
		if rw.MockHeader != "" {
			ml += fmt.Sprintf(` header="%s"`, rw.MockHeader)
		}
		return ml

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

	// Loon cron format: cron "expression" script-path=..., tag=name
	if rw.ScriptType == "cron" {
		cronexp := rw.CronExp
		if cronexp == "" {
			cronexp = "0 0 * * *"
		}
		cronexp = strings.ReplaceAll(cronexp, `"`, "")
		return fmt.Sprintf(`cron "%s" script-path=%s, timeout=%d, tag=%s`, cronexp, rw.ScriptPath, timeout, scriptName)
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
		out.CategoryKey, out.CategoryValue = CategoryForOutput(&mod, false)
		out.MetaExtra = append(out.MetaExtra, mod.MetaExtra...)
		out.SgArg = append(out.SgArg, mod.SgArg...)
		out.BodyRewrites = append(out.BodyRewrites, mod.BodyRewrites...)
		out.ConditionalMITMKey = mod.ConditionalMITMKey
		out.SkipProxy = append(out.SkipProxy, mod.SkipProxy...)
		out.RealIP = append(out.RealIP, mod.RealIP...)
		if mod.HNAddMethod != "" {
			out.HNAddMethod = mod.HNAddMethod
		}
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

	return applyArgumentsTemplate(p.formatStashOutput(out), out.SgArg, "stash")
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
	if out.CategoryKey != "" && out.CategoryValue != "" {
		sb.WriteString(fmt.Sprintf("#!%s=%s\n", out.CategoryKey, out.CategoryValue))
	}
	for _, m := range out.MetaExtra {
		sb.WriteString(m + "\n")
	}
	// Surge #!arguments metadata: key:value,...
	if len(out.SgArg) > 0 {
		var parts []string
		for _, a := range out.SgArg {
			val := strings.TrimSpace(strings.Split(a.Value, ",")[0])
			parts = append(parts, a.Key+":"+val)
		}
		sb.WriteString(fmt.Sprintf("#!arguments=%s\n", strings.Join(parts, ",")))
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

		if len(out.BodyRewrites) > 0 {
			sb.WriteString("  body-rewrite:\n")
			for _, br := range out.BodyRewrites {
				sb.WriteString(fmt.Sprintf("    - %s %s %s\n", br.Type, br.Regex, br.Value))
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
		// Stash YAML format: hostname = %APPEND% "host1"\n    - "host2"
		addMethod := "%APPEND%"
		for _, mod := range modules {
			if mod.HNAddMethod != "" {
				addMethod = mod.HNAddMethod
				break
			}
		}
		hostnameStr := strings.Join(mitm, `"`+"\n    - "+`"`)
		sb.WriteString("hostname = " + addMethod + " \"" + hostnameStr + "\"\n")
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
