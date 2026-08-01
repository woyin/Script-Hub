package rewrite

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/script-hub-org/script-hub/internal/util"
)

func (p *Parser) convertToSurgeFormat(modules []ParsedModule, target string, args map[string]string) string {
	out := surgeOutput{}
	synMitm := util.IsTrue(args["synMitm"])
	delComments := util.IsTrue(args["del"])

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
			p.classifySurgeRewrite(rw, &out, target, args)
		}
		for _, rw := range mod.Scripts {
			p.classifySurgeScript(rw, &out, target, args)
		}
		// Panels (Surge only)
		for _, panel := range mod.Panels {
			out.Panels = append(out.Panels, formatSurgePanel(panel))
		}
		// Hosts
		for _, host := range mod.Hosts {
			out.Hosts = append(out.Hosts, fmt.Sprintf("%s = %s", host.Domain, host.Value))
			out.HostEntries = append(out.HostEntries, host)
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
// `target` distinguishes Surge-strict (surge/egern/lancex: Map Local only for
// suffix-rejects) from Shadowrocket (URL Rewrite with video->img, tinygif kept)
// and Stash (no Map Local; URL Rewrite with -video|-tinygif -> -img). Mirrors
// upstream Rewrite-Parser.js lines 1268-1320.
func (p *Parser) classifySurgeRewrite(rw ParsedRewrite, out *surgeOutput, target string, args map[string]string) {
	isShadowrocket := strings.Contains(target, "shadowrocket")
	isStash := strings.Contains(target, "stash")
	isSurgeStrict := !isShadowrocket && !isStash // surge / egern / lancex
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
		// Upstream Rewrite-Parser.js URL Rewrite reject handling (lines 1268-1320):
		//   - Shadowrocket: emit URL Rewrite line; reject-video -> reject-img (1271);
		//     reject-tinygif kept (1273); others kept as-is.
		//   - Stash: emit URL Rewrite line with -video|-tinygif -> -img (1305).
		//   - Surge-strict (surge/egern/lancex): only plain reject emits a URL Rewrite
		//     line (rwtype matches /(?:reject|302|307|header)$/); suffix-rejects go to
		//     [Map Local] only (lines 1312-1320).
		switch rw.Type {
		case RewriteTypeRejectVideo:
			if isSurgeStrict {
				out.MapLocal = append(out.MapLocal,
					fmt.Sprintf(`%s data-type=tiny-gif status-code=200`, rw.Pattern))
			} else {
				out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject-img", rw.Pattern))
			}
		case RewriteTypeRejectDict:
			if isSurgeStrict {
				out.MapLocal = append(out.MapLocal,
					fmt.Sprintf(`%s data-type=text data="{}" status-code=200 header="Content-Type:application/json"`, rw.Pattern))
			} else {
				out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject-dict", rw.Pattern))
			}
		case RewriteTypeRejectArray:
			if isSurgeStrict {
				out.MapLocal = append(out.MapLocal,
					fmt.Sprintf(`%s data-type=text data="[]" status-code=200`, rw.Pattern))
			} else {
				out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject-array", rw.Pattern))
			}
		case RewriteTypeReject200:
			if isSurgeStrict {
				out.MapLocal = append(out.MapLocal,
					fmt.Sprintf(`%s data-type=text data=" " status-code=200`, rw.Pattern))
			} else {
				out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject-200", rw.Pattern))
			}
		case RewriteTypeRejectImg:
			if isSurgeStrict {
				out.MapLocal = append(out.MapLocal,
					fmt.Sprintf(`%s data-type=tiny-gif status-code=200`, rw.Pattern))
			} else {
				out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject-img", rw.Pattern))
			}
		case RewriteTypeRejectTinyGif:
			if isSurgeStrict {
				out.MapLocal = append(out.MapLocal,
					fmt.Sprintf(`%s data-type=tiny-gif status-code=200`, rw.Pattern))
			} else if isStash {
				out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject-img", rw.Pattern))
			} else {
				out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject-tinygif", rw.Pattern))
			}
		case RewriteTypeReject, RewriteTypeRejectDrop:
			out.URLRewrites = append(out.URLRewrites, fmt.Sprintf("%s reject", rw.Pattern))
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
//
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

// classifySurgeScript classifies a script entry into the Surge-compatible
// [Script] section. Field order and conditional fields follow upstream
// Rewrite-Parser.js (Surge/Shadowrocket branches, lines 1524-1602).
//
// Notes:
//   - engine= is Surge-only (NOT Shadowrocket); Egern/LanceX are treated as
//     Surge-compatible and keep engine.
//   - img-url= is never emitted in Surge/Shadowrocket [Script] (only Loon/Stash).
//   - generic and event/cron/dns/rule each have distinct field sets.
func (p *Parser) classifySurgeScript(rw ParsedRewrite, out *surgeOutput, target string, args map[string]string) {
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

	// engine= only on Surge (and Surge-compat Egern/LanceX); NOT Shadowrocket.
	includeEngine := rw.Engine != "" && target != "shadowrocket"

	// Helper to append argument respecting upstream quoting: if the argument
	// contains a comma and is not already quoted, wrap it in double quotes.
	appendArg := func(parts []string) []string {
		if rw.Arguments == "" {
			return parts
		}
		if strings.Contains(rw.Arguments, ",") && !(strings.HasPrefix(rw.Arguments, `"`) && strings.HasSuffix(rw.Arguments, `"`)) {
			return append(parts, fmt.Sprintf(`argument="%s"`, rw.Arguments))
		}
		return append(parts, fmt.Sprintf("argument=%s", rw.Arguments))
	}

	switch rw.ScriptType {
	case "cron":
		cronexp := rw.CronExp
		if cronexp == "" {
			cronexp = "0 0 * * *"
		}
		// Order per upstream: type, cronexp, script-path, script-update-interval, engine, timeout, wake-system, argument
		var parts []string
		parts = append(parts, "type=cron")
		parts = append(parts, fmt.Sprintf("cronexp=%s", cronexp))
		parts = append(parts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
		if rw.ScriptUpdateInterval != "" {
			parts = append(parts, fmt.Sprintf("script-update-interval=%s", rw.ScriptUpdateInterval))
		}
		if includeEngine {
			parts = append(parts, fmt.Sprintf("engine=%s", rw.Engine))
		}
		parts = append(parts, fmt.Sprintf("timeout=%d", timeout))
		if rw.WakeSystem {
			parts = append(parts, "wake-system=true")
		}
		parts = appendArg(parts)
		out.Scripts = append(out.Scripts, scriptName+" = "+strings.Join(parts, ", "))

	case "event":
		// Order per upstream: type, event-name, script-path, ability, engine, script-update-interval, timeout, argument
		eventName := rw.EventName
		if eventName == "" {
			eventName = "network-changed"
		}
		var parts []string
		parts = append(parts, "type=event")
		parts = append(parts, fmt.Sprintf("event-name=%s", eventName))
		parts = append(parts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
		if rw.Ability != "" {
			parts = append(parts, fmt.Sprintf("ability=%s", rw.Ability))
		}
		if includeEngine {
			parts = append(parts, fmt.Sprintf("engine=%s", rw.Engine))
		}
		if rw.ScriptUpdateInterval != "" {
			parts = append(parts, fmt.Sprintf("script-update-interval=%s", rw.ScriptUpdateInterval))
		}
		parts = append(parts, fmt.Sprintf("timeout=%d", timeout))
		parts = appendArg(parts)
		out.Scripts = append(out.Scripts, scriptName+" = "+strings.Join(parts, ", "))

	case "dns", "rule":
		// Order per upstream: type, script-path, script-update-interval, engine, timeout, argument
		var parts []string
		parts = append(parts, fmt.Sprintf("type=%s", rw.ScriptType))
		parts = append(parts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
		if rw.ScriptUpdateInterval != "" {
			parts = append(parts, fmt.Sprintf("script-update-interval=%s", rw.ScriptUpdateInterval))
		}
		if includeEngine {
			parts = append(parts, fmt.Sprintf("engine=%s", rw.Engine))
		}
		parts = append(parts, fmt.Sprintf("timeout=%d", timeout))
		parts = appendArg(parts)
		out.Scripts = append(out.Scripts, scriptName+" = "+strings.Join(parts, ", "))

	case "generic":
		// Upstream pushes generic scripts as-is to otherRule (line 1571-1572);
		// they are not normal [Script] entries on Surge/Shadowrocket. Emit nothing
		// here so they are not mis-rendered. (Stash handles generic as tiles.)
		return

	default:
		// http-request / http-response (and any other pattern-based type).
		// Order per upstream: type, pattern, script-path, requires-body,
		// binary-body-mode, engine, max-size, ability, script-update-interval,
		// timeout, argument
		var parts []string
		parts = append(parts, fmt.Sprintf("type=%s", rw.ScriptType))
		if rw.Pattern != "" {
			parts = append(parts, fmt.Sprintf("pattern=%s", rw.Pattern))
		}
		parts = append(parts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
		parts = append(parts, fmt.Sprintf("requires-body=%d", requiresBody))
		if rw.BinaryBody {
			parts = append(parts, "binary-body-mode=true")
		}
		if includeEngine {
			parts = append(parts, fmt.Sprintf("engine=%s", rw.Engine))
		}
		if rw.MaxSize != "" {
			parts = append(parts, fmt.Sprintf("max-size=%s", rw.MaxSize))
		}
		if rw.Ability != "" {
			parts = append(parts, fmt.Sprintf("ability=%s", rw.Ability))
		}
		if rw.ScriptUpdateInterval != "" {
			parts = append(parts, fmt.Sprintf("script-update-interval=%s", rw.ScriptUpdateInterval))
		}
		parts = append(parts, fmt.Sprintf("timeout=%d", timeout))
		parts = appendArg(parts)
		out.Scripts = append(out.Scripts, scriptName+" = "+strings.Join(parts, ", "))
	}
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
