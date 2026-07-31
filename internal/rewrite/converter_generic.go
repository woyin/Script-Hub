package rewrite

import (
	"fmt"
	"strings"
)

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
