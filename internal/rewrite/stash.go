// stash.go contains the Stash-specific target converters.
//
// Stash is Surge-compatible at the IR level (it reuses surgeOutput and
// classifySurgeRewrite) but diverges in two ways:
//   1. Scripts use Stash-native YAML field names and structure
//      (classifyStashScript), not the Surge [Script] line format.
//   2. Final serialization is YAML (formatStashOutput), not INI.
//
// These two functions are kept here rather than in converter.go to make the
// Stash-specific logic easy to find and review.

package rewrite

import (
	"fmt"
	"strings"

	"github.com/script-hub-org/script-hub/internal/util"
)

// parsed rewrite, matching upstream Rewrite-Parser.js (Stash branch, lines
// 1646-1745). Stash uses its own field names (match, name, type, require-body,
// max-size, binary-mode, timeout, argument) and structure, so we do NOT reuse
// classifySurgeScript's Surge-style line output.
//
// Per upstream:
//   - http-request/http-response -> http.script entry + script-provider
//   - cron                       -> cron.script entry + script-provider
//   - generic                    -> tiles entry + script-provider (unless raw)
//   - event/network-changed/rule/dns -> pushed to otherRule (passthrough); we
//     emit nothing here (no native Stash representation in this IR).
//   - script names get a "_<num>" suffix and are deduplicated by URL.
func (p *Parser) classifyStashScript(rw ParsedRewrite, out *surgeOutput, args map[string]string) {
	timeout := rw.Timeout
	if timeout == 0 {
		timeout = 30
	}

	scriptName := rw.Replacement
	if scriptName == "" {
		scriptName = sanitizeName(rw.Pattern)
	}
	if rw.Tag != "" {
		scriptName = rw.Tag
	}

	// Upstream appends "_<num>" to each script name and dedups by URL
	// (line 1652). We approximate with a per-output counter keyed by name.
	if out.stashNameIdx == nil {
		out.stashNameIdx = map[string]int{}
	}
	idx := out.stashNameIdx[scriptName]
	out.stashNameIdx[scriptName] = idx + 1
	stashName := scriptName
	if idx > 0 {
		stashName = fmt.Sprintf("%s_%d", scriptName, idx)
	}

	jsurl := rw.ScriptPath
	provider := fmt.Sprintf("  \"%s\":\n    url: %s\n    interval: 86400", stashName, jsurl)

	switch {
	case rw.ScriptType == "http-request" || rw.ScriptType == "http-response":
		// type strips the "http-" prefix on Stash (line 1676: jstype.replace(/http-/,'')).
		stashType := strings.TrimPrefix(rw.ScriptType, "http-")
		var lines []string
		lines = append(lines, "    - match: "+rw.Pattern)
		lines = append(lines, `      name: "`+stashName+`"`)
		lines = append(lines, "      type: "+stashType)
		if rw.RequiresBody {
			lines = append(lines, "      require-body: true")
		}
		if rw.MaxSize != "" {
			lines = append(lines, "      max-size: "+rw.MaxSize)
		}
		if rw.BinaryBody {
			lines = append(lines, "      binary-mode: true")
		}
		lines = append(lines, fmt.Sprintf("      timeout: %d", timeout))
		if rw.Arguments != "" {
			lines = append(lines, "      argument: |-\n        "+rw.Arguments)
		}
		out.StashScripts = append(out.StashScripts, strings.Join(lines, "\n"))
		out.StashProviders = append(out.StashProviders, provider)

	case rw.ScriptType == "cron":
		cronexp := rw.CronExp
		if cronexp == "" {
			cronexp = "0 0 * * *"
		}
		cronexp = strings.ReplaceAll(cronexp, `"`, "")
		var lines []string
		lines = append(lines, `    - name: "`+stashName+`"`)
		lines = append(lines, "      cron: "+cronexp)
		lines = append(lines, fmt.Sprintf("      timeout: %d", timeout))
		if rw.Arguments != "" {
			lines = append(lines, "      argument: |-\n        "+rw.Arguments)
		}
		out.StashCron = append(out.StashCron, strings.Join(lines, "\n"))
		out.StashProviders = append(out.StashProviders, provider)

	case rw.ScriptType == "generic":
		// Tiles output. Default background color matches upstream.
		bgColor := "#5d84f8"
		tilesTargets := util.GetArgArr(args["tiles"])
		tilesColors := util.GetArgArr(args["tcolor"])
		for i, t := range tilesTargets {
			if t == scriptName && i < len(tilesColors) {
				bgColor = strings.ReplaceAll(tilesColors[i], "@", "#")
				break
			}
		}
		icon := rw.ImgURL
		var lines []string
		lines = append(lines, `  - name: "`+stashName+`"`)
		lines = append(lines, `    interval: 3600`)
		lines = append(lines, `    title: "`+stashName+`"`)
		lines = append(lines, `    icon: "`+icon+`"`)
		lines = append(lines, `    backgroundColor: "`+bgColor+`"`)
		lines = append(lines, fmt.Sprintf("    timeout: %d", timeout))
		if rw.Arguments != "" {
			lines = append(lines, "    argument: |-\n      "+rw.Arguments)
		}
		out.StashTiles = append(out.StashTiles, strings.Join(lines, "\n"))
		out.StashProviders = append(out.StashProviders, provider)

	default:
		// event / network-changed / rule / dns: upstream pushes the original
		// line to otherRule. We have no raw line in the IR, so emit nothing.
	}
}

// --- Stash ---

// convertToStashFormat builds the Stash stoverride YAML by reusing the Surge
// IR (surgeOutput) and then emitting it through formatStashOutput. Stash is
// Surge-compatible at the IR level but diverges in serialization (YAML with
// multi-line string blocks) and in script/arguments handling.
func (p *Parser) convertToStashFormat(modules []ParsedModule, target string, args map[string]string) string {
	out := surgeOutput{}
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
			// Stash has its own native YAML script format; do NOT reuse the
			// Surge-style classifySurgeScript line builder.
			p.classifyStashScript(rw, &out, args)
		}
		// Hosts: Stash has no [Host] section; upstream pushes every host
		// entry to otherRule (Rewrite-Parser.js line 1392-1393).
		for _, host := range mod.Hosts {
			out.Rules = append(out.Rules, host.Raw)
		}
	}

	out.MITM = uniqueStrings(out.MITM)

	if util.IsTrue(args["synMitm"]) {
		out.ForceHTTPHosts = append(out.ForceHTTPHosts, out.MITM...)
	}

	if delComments {
		out.URLRewrites = filterCommented(out.URLRewrites)
		out.HeaderRewrites = filterCommented(out.HeaderRewrites)
		out.Scripts = filterCommented(out.Scripts)
		out.Rules = filterCommented(out.Rules)
	}

	return applyArgumentsTemplate(p.formatStashOutput(out, modules, args), out.SgArg, "stash")
}

// formatStashOutput serializes the Surge IR into Stash's YAML stoverride format:
//   - top-level metadata uses YAML block scalars (name: |-\n  ...)
//   - [Script] / [URL Rewrite] / [Header Rewrite] / [MITM] become YAML list keys
//   - #!arguments are emitted via applyArgumentsTemplate("stash") substitution
func (p *Parser) formatStashOutput(out surgeOutput, modules []ParsedModule, args map[string]string) string {
	var sb strings.Builder

	// Stash uses YAML format for metadata (name: |-\n  value)
	if out.Name != "" {
		sb.WriteString("name: |-\n  " + out.Name + "\n")
	}
	if out.Desc != "" {
		sb.WriteString("desc: |-\n  " + out.Desc + "\n")
	}
	if out.Icon != "" {
		sb.WriteString("icon: |-\n  " + out.Icon + "\n")
	}
	if out.CategoryKey != "" && out.CategoryValue != "" {
		catKey := out.CategoryKey
		if catKey == "category" {
			catKey = "category"
		}
		sb.WriteString(catKey + ": |-\n  " + out.CategoryValue + "\n")
	}
	for _, m := range out.MetaExtra {
		// Convert #!key=value to key: |-\n  value for Stash YAML
		if strings.HasPrefix(m, "#!") && strings.Contains(m, "=") {
			kv := strings.SplitN(m[2:], "=", 2)
			sb.WriteString(kv[0] + ": |-\n  " + kv[1] + "\n")
		} else {
			sb.WriteString(m + "\n")
		}
	}
	// Surge #!arguments metadata
	if len(out.SgArg) > 0 {
		var parts []string
		for _, a := range out.SgArg {
			val := strings.TrimSpace(strings.Split(a.Value, ",")[0])
			parts = append(parts, a.Key+":"+val)
		}
		sb.WriteString("arguments: |-\n  " + strings.Join(parts, ",") + "\n")
	}
	sb.WriteString("\n")

	// Rules section
	if len(out.Rules) > 0 {
		sb.WriteString("rules:\n")
		for _, r := range out.Rules {
			sb.WriteString("  - " + r + "\n")
		}
		sb.WriteString("\n")
	}

	// Scripts were classified into Stash-native buffers by classifyStashScript.
	tiles := out.StashTiles
	cronEntries := out.StashCron
	providers := out.StashProviders
	stashScripts := out.StashScripts

	// HTTP frame
	hasHTTP := len(out.MITM) > 0 || len(out.HeaderRewrites) > 0 ||
		len(out.URLRewrites) > 0 || len(stashScripts) > 0 ||
		len(out.BodyRewrites) > 0 || len(out.MapLocal) > 0 ||
		len(out.ForceHTTPHosts) > 0

	if hasHTTP {
		sb.WriteString("http:\n")

		if len(out.ForceHTTPHosts) > 0 {
			addMethod := "%APPEND%"
			for _, mod := range modules {
				if mod.FHEAddMethod != "" {
					addMethod = mod.FHEAddMethod
					break
				}
			}
			sb.WriteString("  force-http-engine-hosts: " + addMethod + " " + strings.Join(out.ForceHTTPHosts, ", ") + "\n")
		}

		if len(out.MITM) > 0 {
			sb.WriteString("  mitm:\n")
			for _, h := range out.MITM {
				sb.WriteString("    - \"" + h + "\"\n")
			}
		}

		if len(out.HeaderRewrites) > 0 {
			sb.WriteString("  header-rewrite:\n")
			for _, r := range out.HeaderRewrites {
				// Stash normalizes Surge header-rewrite lines per upstream
				// (lines 1382-1385): strip http-request/http-response prefix
				// and replace " header-" with " request-"/" response-".
				sb.WriteString("    - >-\n      " + convertSurgeHeaderRewriteToStash(r) + "\n")
			}
		}

		if len(out.URLRewrites) > 0 {
			sb.WriteString("  url-rewrite:\n")
			for _, r := range out.URLRewrites {
				// Per upstream Rewrite-Parser.js line 1305, Stash maps
				// reject-video / reject-tinygif -> reject-img, and
				// header -> transparent. classifySurgeRewrite already routes
				// reject-video to reject-tinygif (Surge/Shadowrocket mapping),
				// so we normalize here for Stash-specific output.
				normalized := r
				normalized = strings.ReplaceAll(normalized, "reject-tinygif", "reject-img")
				normalized = strings.ReplaceAll(normalized, "reject-video", "reject-img")
				normalized = strings.ReplaceAll(normalized, " header", " transparent")
				sb.WriteString("    - >-\n      " + normalized + "\n")
			}
		}

		if len(out.MapLocal) > 0 {
			for _, r := range out.MapLocal {
				sb.WriteString("  # map-local: " + r + "\n")
			}
		}

		if len(out.BodyRewrites) > 0 {
			sb.WriteString("  body-rewrite:\n")
			for _, br := range out.BodyRewrites {
				// Stash body-rewrite type mapping per upstream Rewrite-Parser.js
				// (line 1848): strip leading "http-", then replace a bare
				// "request"/"response" with "request-replace-regex"/
				// "response-replace-regex". jq variants ("request-jq"/"response-jq")
				// pass through unchanged.
				brType := strings.TrimPrefix(br.Type, "http-")
				switch brType {
				case "request":
					brType = "request-replace-regex"
				case "response":
					brType = "response-replace-regex"
				}
				value := stripMatchingOuterQuotes(br.Value)
				sb.WriteString("    - >-\n      " + br.Regex + " " + brType + " " + value + "\n")
			}
		}

		if len(stashScripts) > 0 {
			sb.WriteString("  script:\n")
			for _, s := range stashScripts {
				sb.WriteString(s + "\n")
			}
		}
	}

	// Tiles (generic scripts)
	if len(tiles) > 0 {
		sb.WriteString("tiles:\n")
		for _, t := range tiles {
			sb.WriteString(t + "\n")
		}
	}

	// Cron section
	if len(cronEntries) > 0 {
		sb.WriteString("cron:\n  script:\n")
		for _, c := range cronEntries {
			sb.WriteString(c + "\n")
		}
	}

	// Script providers (deduplicated)
	seenProviders := make(map[string]bool)
	var uniqueProviders []string
	for _, pr := range providers {
		if !seenProviders[pr] {
			seenProviders[pr] = true
			uniqueProviders = append(uniqueProviders, pr)
		}
	}
	if len(uniqueProviders) > 0 {
		sb.WriteString("script-providers:\n")
		for _, pr := range uniqueProviders {
			sb.WriteString(pr + "\n")
		}
	}

	return sb.String()
}
