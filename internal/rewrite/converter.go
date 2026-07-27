// Input: fmt, net/url, regexp, strings, internal/util
// Output: func (Parser) convertModules(), Surge/Loon/Stash/Generic 各格式转换与格式化函数, 工具函数（uniqueStrings/filterCommented 等）
// Pos: 业务层-重写格式转换，将统一中间表示转换为目标平台输出格式
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

package rewrite

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/script-hub-org/script-hub/internal/util"
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

// --- Surge / Shadowrocket ---

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

// --- Loon ---

func (p *Parser) convertToLoonFormat(modules []ParsedModule, target string, args map[string]string) string {
	var rules, rewrites, scripts, mitm []string
	var name, desc, icon string
	var catKey, catValue string
	var metaExtra []string
	var sgArg []SgArgument
	var modInputBox []InputBoxEntry
	var bodyRewrites []BodyRewriteEntry
	var skipProxy, realIP []string
	var loonHosts []string
	delComments := util.IsTrue(args["del"])

	for _, mod := range modules {
		name = mod.Name
		desc = mod.Desc
		icon = mod.Icon
		catKey, catValue = CategoryForOutput(&mod, true)
		metaExtra = append(metaExtra, mod.MetaExtra...)
		modInputBox = append(modInputBox, mod.ModInputBox...)
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
		// Hosts: normal host → [Host]; script host → [Rule] (otherRule per
		// upstream Rewrite-Parser.js line 1395-1397).
		for _, host := range mod.Hosts {
			if strings.Contains(host.Value, "script:") {
				rules = append(rules, host.Raw)
			} else {
				loonHosts = append(loonHosts, fmt.Sprintf("%s = %s", host.Domain, host.Value))
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
	for _, ib := range modInputBox {
		sb.WriteString("#!" + ib.Key + ib.Value + "\n")
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
	if len(loonHosts) > 0 {
		sb.WriteString("[Host]\n")
		for _, h := range loonHosts {
			sb.WriteString(h + "\n")
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
	synMitm := util.IsTrue(args["synMitm"])
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
		// Loon [Rewrite] uses "PATTERN reject-type" (not the QX-style "url-reject").
		// Per upstream Rewrite-Parser.js, reject-tinygif → reject-img on Loon;
		// reject / reject-dict / reject-img / reject-200 / reject-array pass through.
		// reject-video / reject-drop have no Loon equivalent → plain reject.
		switch rw.Type {
		case RewriteTypeRejectDict:
			return fmt.Sprintf("%s reject-dict", rw.Pattern)
		case RewriteTypeRejectImg:
			return fmt.Sprintf("%s reject-img", rw.Pattern)
		case RewriteTypeRejectTinyGif:
			return fmt.Sprintf("%s reject-img", rw.Pattern)
		case RewriteTypeReject200:
			return fmt.Sprintf("%s reject-200", rw.Pattern)
		case RewriteTypeRejectArray:
			return fmt.Sprintf("%s reject-array", rw.Pattern)
		default:
			return fmt.Sprintf("%s reject", rw.Pattern)
		}

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
		// Loon mock-response-body / mock-request-body.
		// Upstream Rewrite-Parser.js (loon-plugin branch) emits NO header field;
		// data precedence is data-path (datapath) > data > mockurl (as data-path).
		mockBodyType := "mock-response-body"
		if rw.Type == RewriteTypeMockRequestBody {
			mockBodyType = "mock-request-body"
		}
		ml := fmt.Sprintf("%s %s", rw.Pattern, mockBodyType)
		if rw.MockType != "" {
			ml += fmt.Sprintf(" data-type=%s", rw.MockType)
		}
		if rw.MockDataPath != "" {
			ml += fmt.Sprintf(" data-path=%s", rw.MockDataPath)
		} else if rw.MockData != "" {
			ml += fmt.Sprintf(` data="%s"`, rw.MockData)
		}
		if rw.MockStatus != "" {
			ml += fmt.Sprintf(" status-code=%s", rw.MockStatus)
		}
		if rw.MockBase64 {
			ml += " mock-data-is-base64=true"
		}
		return ml

	default:
		return ""
	}
}

// convertSurgeHeaderRewriteToStash converts a Surge [Header Rewrite] line to a
// Stash header-rewrite entry.
//
// Upstream Rewrite-Parser.js (stash-stoverride branch, lines 1382-1385) only
// ever runs this transform on lines that were normalized into rwhdBox with a
// leading "http-request "/"http-response " prefix — and that normalization only
// happens for Loon-plugin sources (lines 678-728). Surge [Header Rewrite] raw
// lines (e.g. "^url header-request header-add K V") are NOT parsed into rwhdBox
// upstream at all, so upstream silently drops them for Stash/Loon targets.
//
// To stay faithful while still surfacing the rule to the user, we:
//   - Apply the upstream transform (strip http-{request,response} prefix, then
//     replace the first " header-" with " request-"/" response-") when the line
//     is in the normalized (Loon-origin) form.
//   - Otherwise (Surge-origin raw line), emit the line commented-out so the user
//     sees it but Stash does not receive a malformed entry. This matches the
//     upstream "silently dropped" outcome while being more transparent.
func convertSurgeHeaderRewriteToStash(line string) string {
	isResponse := strings.HasPrefix(line, "http-response ")
	if strings.HasPrefix(line, "http-request ") {
		line = strings.TrimPrefix(line, "http-request ")
	} else if strings.HasPrefix(line, "http-response ") {
		line = strings.TrimPrefix(line, "http-response ")
	} else {
		// Surge-origin raw line: not convertible by the upstream transform.
		return "# " + line
	}
	hdtype := " request-"
	if isResponse {
		hdtype = " response-"
	}
	if idx := strings.Index(line, " header-"); idx >= 0 {
		line = line[:idx] + hdtype + line[idx+len(" header-"):]
	}
	return line
}

// convertSurgeHeaderRewriteToLoon converts a Surge [Header Rewrite] line to a
// Loon [Rewrite] entry.
//
// Upstream Rewrite-Parser.js (loon-plugin branch, lines 1361-1366) only ever
// runs this transform on rwhdBox entries that were normalized with a leading
// "http-request "/"http-response " prefix — and that normalization only happens
// for Loon-plugin sources (lines 678-728). Surge [Header Rewrite] raw lines are
// NOT parsed into rwhdBox upstream, so they are silently dropped for Loon.
//
// To stay faithful while still surfacing the rule to the user:
//   - For normalized (Loon-origin) form, apply the upstream transform (strip
//     http-{request,response} prefix; for response rewrites, replace the first
//     " header-" with " response-header-").
//   - For Surge-origin raw lines, emit commented-out so the Loon [Rewrite]
//     section does not receive a malformed entry.
func convertSurgeHeaderRewriteToLoon(line string) string {
	isResponse := strings.HasPrefix(line, "http-response ")
	if strings.HasPrefix(line, "http-request ") {
		line = strings.TrimPrefix(line, "http-request ")
	} else if strings.HasPrefix(line, "http-response ") {
		line = strings.TrimPrefix(line, "http-response ")
	} else {
		return "# " + line
	}
	if isResponse {
		if idx := strings.Index(line, " header-"); idx >= 0 {
			line = line[:idx] + " response-header-" + line[idx+len(" header-"):]
		}
	}
	return line
}

// convertLoonScript converts a script entry to a Loon [Script] line. Generic
// scripts (Surge "generic" tile type) are dropped here per upstream JS, since
// Loon surfaces them differently. Cron scripts use the Loon cron form:
//   cron "<expr>" script-path=URL, timeout=N, tag=name, enable=true, img-url=...
// Other types map to type=http-{request,response}[,requires-body=true].
func (p *Parser) convertLoonScript(rw ParsedRewrite) string {
	// Per upstream Rewrite-Parser.js, Loon does not emit [Script] entries for
	// "generic" tiles (those are pushed as-is to otherRule / handled as tiles).
	// Return empty so the Loon converter skips this entry in [Script].
	if rw.ScriptType == "generic" {
		return ""
	}

	timeout := rw.Timeout
	if timeout == 0 {
		timeout = 30
	}

	scriptName := rw.Replacement
	if scriptName == "" {
		scriptName = sanitizeName(rw.Pattern)
	}

	// Loon cron format: cron "expression" script-path=URL, timeout=N, tag=name, enable=..., img-url=..., argument=...
	if rw.ScriptType == "cron" {
		cronexp := rw.CronExp
		if cronexp == "" {
			cronexp = "0 0 * * *"
		}
		cronexp = strings.ReplaceAll(cronexp, `"`, "")
		parts := []string{
			fmt.Sprintf(`cron "%s" script-path=%s`, cronexp, rw.ScriptPath),
			fmt.Sprintf("timeout=%d", timeout),
			fmt.Sprintf("tag=%s", scriptName),
		}
		if rw.Enable {
			parts = append(parts, "enable=true")
		}
		if rw.ImgURL != "" {
			parts = append(parts, fmt.Sprintf("img-url=%s", rw.ImgURL))
		}
		if rw.Arguments != "" {
			parts = append(parts, fmt.Sprintf("argument=%s", rw.Arguments))
		}
		return strings.Join(parts, ", ")
	}

	// Surge "event" maps to Loon "network-changed" (upstream lines 1489-1490).
	// network-changed has no pattern.
	// Surge "event" maps to Loon "network-changed" (upstream lines 1489-1490);
	// network-changed has no pattern. http-request / http-response keep their
	// type name verbatim.
	scriptType := rw.ScriptType
	pattern := rw.Pattern
	if scriptType == "event" {
		scriptType = "network-changed"
		pattern = ""
	}
	// Loon-origin network-changed scripts already carry ScriptType="network-changed"
	// and store "network-changed" as the Pattern; drop the redundant pattern so the
	// round-trip output is "network-changed script-path=..." not "network-changed network-changed ...".
	if scriptType == "network-changed" {
		pattern = ""
	}

	// Field order per upstream Loon branch (lines 1604-1614):
	// script-path, requires-body, binary-body-mode, timeout, tag, enable, img-url, argument
	var opts []string
	opts = append(opts, fmt.Sprintf("script-path=%s", rw.ScriptPath))
	if rw.RequiresBody {
		opts = append(opts, "requires-body=true")
	}
	if rw.BinaryBody {
		opts = append(opts, "binary-body-mode=true")
	}
	opts = append(opts, fmt.Sprintf("timeout=%d", timeout))
	opts = append(opts, fmt.Sprintf("tag=%s", scriptName))
	if rw.Enable {
		opts = append(opts, "enable=true")
	}
	if rw.ImgURL != "" {
		opts = append(opts, fmt.Sprintf("img-url=%s", rw.ImgURL))
	}
	if rw.Arguments != "" {
		opts = append(opts, fmt.Sprintf("argument=%s", rw.Arguments))
	}

	if pattern != "" {
		return fmt.Sprintf("%s %s %s", scriptType, pattern, strings.Join(opts, ", "))
	}
	return fmt.Sprintf("%s %s", scriptType, strings.Join(opts, ", "))
}

// --- Quantumult X ---

// convertToQXFormat converts parsed modules to a Quantumult X rewrite config.
// Output sections: [rewrite_local] (rewrites + http scripts), [task_local] (cron scripts),
// [mitm] (hostname list). Rules and metadata are emitted as comments since QX does not
// carry them in the rewrite config. This closes the plugin-to-plugin conversion matrix:
// any source (Surge/Shadowrocket/Loon/Stash/Egern/LanceX) can be turned into a QX rewrite.
func (p *Parser) convertToQXFormat(modules []ParsedModule, target string, args map[string]string) string {
	var rewrites []string // [rewrite_local]
	var tasks []string    // [task_local]
	var mitm []string
	var rules []string
	var hosts []string
	var name, desc, icon string
	delComments := util.IsTrue(args["del"])

	for _, mod := range modules {
		name = mod.Name
		desc = mod.Desc
		icon = mod.Icon
		mitm = append(mitm, mod.MITM...)
		rules = append(rules, mod.Rules...)
		for _, host := range mod.Hosts {
			hosts = append(hosts, fmt.Sprintf("%s = %s", host.Domain, host.Value))
		}

		for _, rw := range mod.Rewrites {
			if line := p.convertQXRewrite(rw); line != "" {
				rewrites = append(rewrites, line)
			}
		}
		// Body rewrite entries: emit as QX body rewrite lines
		for _, br := range mod.BodyRewrites {
			if line := qxBodyRewrite(br); line != "" {
				rewrites = append(rewrites, line)
			}
		}
		// Scripts: cron → [task_local], http → [rewrite_local]
		for _, rw := range mod.Scripts {
			if line := p.convertQXScript(rw); line != "" {
				if rw.ScriptType == "cron" {
					tasks = append(tasks, line)
				} else {
					rewrites = append(rewrites, line)
				}
			}
		}
	}

	mitm = uniqueStrings(mitm)

	if delComments {
		rewrites = filterCommented(rewrites)
		tasks = filterCommented(tasks)
		rules = filterCommented(rules)
	}

	var sb strings.Builder
	// Metadata as comments (QX rewrite config has no module metadata syntax)
	if name != "" {
		sb.WriteString("#!name=" + name + "\n")
	}
	if desc != "" {
		sb.WriteString("#!desc=" + desc + "\n")
	}
	if icon != "" {
		sb.WriteString("#!icon=" + icon + "\n")
	}
	sb.WriteString("\n")

	if len(rules) > 0 {
		// QX carries rules in [filter_local] / [filter_remote]; emit as comment block
		// so the user can copy them into the right section of the QX config.
		sb.WriteString("# 规则（请按需复制到 [filter_local]）：\n")
		for _, r := range rules {
			sb.WriteString("# " + r + "\n")
		}
		sb.WriteString("\n")
	}

	if len(rewrites) > 0 {
		sb.WriteString("[rewrite_local]\n")
		for _, r := range rewrites {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}

	if len(tasks) > 0 {
		sb.WriteString("[task_local]\n")
		for _, t := range tasks {
			sb.WriteString(t + "\n")
		}
		sb.WriteString("\n")
	}

	if len(hosts) > 0 {
		sb.WriteString("[host]\n")
		for _, h := range hosts {
			sb.WriteString(h + "\n")
		}
		sb.WriteString("\n")
	}

	if len(mitm) > 0 {
		sb.WriteString("[mitm]\n")
		sb.WriteString("hostname = " + strings.Join(mitm, ", ") + "\n")
	}

	return sb.String()
}

// convertQXRewrite converts a rewrite entry to a Quantumult X rewrite_local line.
// QX format: PATTERN url TYPE [args]
func (p *Parser) convertQXRewrite(rw ParsedRewrite) string {
	switch rw.Type {
	case RewriteTypeRequestHeader:
		// QX native: PATTERN url request-header MATCH request-header REPLACE
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url request-header %s request-header %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		parts := strings.SplitN(rw.Replacement, "->", 2)
		if len(parts) == 2 {
			return fmt.Sprintf("%s url request-header %s request-header %s", rw.Pattern, parts[0], parts[1])
		}
		return fmt.Sprintf("%s url request-header %s request-header %s", rw.Pattern, rw.Replacement, rw.Replacement)

	case RewriteTypeResponseHeader:
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url response-header %s response-header %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		parts := strings.SplitN(rw.Replacement, "->", 2)
		if len(parts) == 2 {
			return fmt.Sprintf("%s url response-header %s response-header %s", rw.Pattern, parts[0], parts[1])
		}
		return fmt.Sprintf("%s url response-header %s response-header %s", rw.Pattern, rw.Replacement, rw.Replacement)

	case RewriteTypeRequestBody:
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url request-body %s request-body %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		return fmt.Sprintf("%s url request-body %s request-body %s", rw.Pattern, rw.Replacement, rw.Replacement)

	case RewriteTypeResponseBody:
		if rw.MatchPart != "" && rw.ReplacePart != "" {
			return fmt.Sprintf("%s url response-body %s response-body %s", rw.Pattern, rw.MatchPart, rw.ReplacePart)
		}
		return fmt.Sprintf("%s url response-body %s response-body %s", rw.Pattern, rw.Replacement, rw.Replacement)

	case RewriteTypeReject, RewriteTypeRejectDict, RewriteTypeRejectImg,
		RewriteTypeRejectTinyGif, RewriteTypeReject200, RewriteTypeRejectArray,
		RewriteTypeRejectVideo, RewriteTypeRejectDrop:
		// QX supports reject / reject-dict / reject-img / reject-tinygif / reject-200 / reject-array.
		// reject-drop / reject-video are not native QX types; fall back to reject.
		switch rw.Type {
		case RewriteTypeRejectDict:
			return fmt.Sprintf("%s url reject-dict", rw.Pattern)
		case RewriteTypeRejectImg:
			return fmt.Sprintf("%s url reject-img", rw.Pattern)
		case RewriteTypeRejectTinyGif:
			return fmt.Sprintf("%s url reject-tinygif", rw.Pattern)
		case RewriteTypeReject200:
			return fmt.Sprintf("%s url reject-200", rw.Pattern)
		case RewriteTypeRejectArray:
			return fmt.Sprintf("%s url reject-array", rw.Pattern)
		default:
			// RewriteTypeReject, RewriteTypeRejectDrop, RewriteTypeRejectVideo
			return fmt.Sprintf("%s url reject", rw.Pattern)
		}

	case RewriteTypeURLRewrite:
		// Surge/Loon URL rewrite target may be "302 URL" / "307 URL", a bare URL,
		// or already carry a QX-style keyword (request-data / reject-* / 302 ...).
		rep := rw.Replacement
		fields := strings.Fields(rep)
		if len(fields) >= 2 && (fields[0] == "302" || fields[0] == "307") {
			return fmt.Sprintf("%s url %s %s", rw.Pattern, fields[0], strings.Join(fields[1:], " "))
		}
		// Already a QX rewrite keyword + target: emit verbatim after "url".
		if len(fields) >= 2 && isQXRewriteKeyword(fields[0]) {
			return fmt.Sprintf("%s url %s", rw.Pattern, rep)
		}
		if rep != "" {
			// Bare URL redirect: in QX, "<pattern> url <URL>" is a 302 redirect.
			// (request-data would wrongly rewrite the request body.)
			return fmt.Sprintf("%s url %s", rw.Pattern, rep)
		}
		return ""

	case RewriteTypeHeaderRewrite:
		// Surge [Header Rewrite] pass-through line → best-effort QX request/response-header
		return convertSurgeHeaderRewriteToQX(rw.Replacement)

	case RewriteTypeEchoResponse:
		dataType := rw.EchoCT
		if dataType == "" {
			dataType = "text/plain"
		}
		if rw.EchoURL != "" {
			return fmt.Sprintf("%s url echo-response %s %s", rw.Pattern, dataType, rw.EchoURL)
		}
		return fmt.Sprintf("%s url echo-response %s", rw.Pattern, dataType)

	case RewriteTypeMapLocal:
		// No direct QX equivalent; emit as comment for manual handling.
		return "# [Map Local 无 QX 等价] " + rw.Replacement

	case RewriteTypeMock, RewriteTypeMockRequestBody:
		return "# [mock 无原生 QX 等价] " + rw.Pattern + " data-type=" + rw.MockType

	default:
		return ""
	}
}

// qxBodyRewrite converts a BodyRewriteEntry to a QX rewrite_local line.
// jq types → request/response-body-json-jq; replace-regex → request/response-body.
func qxBodyRewrite(br BodyRewriteEntry) string {
	switch br.Type {
	case "http-request-jq":
		return fmt.Sprintf("%s url request-body-json-jq %s", br.Regex, br.Value)
	case "http-response-jq":
		return fmt.Sprintf("%s url response-body-json-jq %s", br.Regex, br.Value)
	case "http-request":
		// Surge/Loon [Body Rewrite] stores only URL pattern + replacement; there is no
		// separate body-match field. QX `request-body MATCH request-body REPLACE` requires
		// an explicit body regex, so a faithful round-trip is impossible. Upstream
		// Rewrite-Parser.js has no QX body-rewrite output branch at all. Emit a comment
		// rather than silently corrupting the rule (using Value as both MATCH and REPLACE
		// would be wrong).
		return fmt.Sprintf("# [Body Rewrite %s %s] no QX equivalent (needs explicit body-match)", br.Regex, br.Value)
	case "http-response":
		return fmt.Sprintf("# [Body Rewrite %s %s] no QX equivalent (needs explicit body-match)", br.Regex, br.Value)
	}
	return ""
}

// convertSurgeHeaderRewriteToQX converts a Surge [Header Rewrite] directive to a QX
// request-header / response-header rewrite. Surge form:
//
//	PATTERN header-rewrite {request,response}-header MATCH REPLACE
func convertSurgeHeaderRewriteToQX(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 5 && parts[1] == "header-rewrite" {
		direction := parts[2] // request-header or response-header
		pattern := parts[0]
		match := parts[3]
		replace := strings.Join(parts[4:], " ")
		qxType := "request-header"
		if strings.HasPrefix(direction, "response") {
			qxType = "response-header"
		}
		return fmt.Sprintf("%s url %s %s %s %s", pattern, qxType, match, qxType, replace)
	}
	return "# [Header Rewrite] " + line
}

// convertQXScript converts a script entry to a QX line.
// http-{request,response}-{header,body} → [rewrite_local]; cron → [task_local].
func (p *Parser) convertQXScript(rw ParsedRewrite) string {
	// Cron script → QX [task_local]: CRON EXPRESSION SCRIPT_PATH, tag=NAME
	if rw.ScriptType == "cron" {
		cronexp := rw.CronExp
		if cronexp == "" {
			cronexp = "0 0 * * *"
		}
		cronexp = strings.ReplaceAll(cronexp, `"`, "")
		line := fmt.Sprintf("%s %s", cronexp, rw.ScriptPath)
		tag := rw.Tag
		if tag == "" {
			tag = rw.Replacement
		}
		if tag == "" {
			tag = sanitizeName(rw.ScriptPath)
		}
		line += ", tag=" + tag
		if rw.Enable {
			line += ", enabled=true"
		}
		if rw.ImgURL != "" {
			line += fmt.Sprintf(", img-url=%s", rw.ImgURL)
		}
		return line
	}

	// http-{request,response}-{header,body} → PATTERN url script-TYPE SCRIPT_PATH
	qxType := qxScriptType(rw.ScriptType, rw.RequiresBody, rw.BodyType)
	if qxType == "" {
		// Unknown / generic script: emit as comment so it is not silently dropped.
		return "# [未识别脚本类型] " + rw.ScriptType + " " + rw.ScriptPath
	}
	if rw.ScriptPath == "" {
		return ""
	}
	return fmt.Sprintf("%s url %s %s", rw.Pattern, qxType, rw.ScriptPath)
}

// qxScriptType maps a Surge/Loon http script type to the QX script- keyword.
// QX keywords: script-request-header, script-request-body,
// script-response-header, script-response-body.
func qxScriptType(scriptType string, requiresBody bool, bodyType string) string {
	t := strings.TrimPrefix(scriptType, "http-")
	switch t {
	case "request":
		if requiresBody || bodyType == "request-body" {
			return "script-request-body"
		}
		return "script-request-header"
	case "response":
		if requiresBody || bodyType == "response-body" {
			return "script-response-body"
		}
		return "script-response-header"
	}
	return ""
}

// isQXRewriteKeyword reports whether kw is a QX rewrite type keyword
// (so a parsed URL-rewrite replacement already in QX form can be emitted verbatim).
func isQXRewriteKeyword(kw string) bool {
	switch kw {
	case "request-header", "response-header", "request-body", "response-body",
		"echo-response", "reject", "reject-dict", "reject-img", "reject-tinygif",
		"reject-200", "reject-array", "request-data", "302", "307":
		return true
	}
	return false
}

// --- Generic fallback ---

// convertToGenericFormat is the fallback target for unknown target apps. It
// emits a best-effort plain-text dump (rewrites, scripts, rules, MITM) without
// any target-specific normalization. Used when targetApp doesn't match any of
// the known clients (surge/loon/qx/stash/...).
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
