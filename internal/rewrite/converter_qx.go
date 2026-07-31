package rewrite

import (
	"fmt"
	"strings"

	"github.com/script-hub-org/script-hub/internal/util"
)

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

