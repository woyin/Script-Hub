package rewrite

import (
	"fmt"
	"strings"

	"github.com/script-hub-org/script-hub/internal/util"
)

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
