package frontend

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed html_part.html htmls_part.html
var frontendFiles embed.FS

//go:embed scripts/Rewrite-Parser.js scripts/rule-parser.js scripts/script-converter.js scripts/scriptMap.js
var convertScripts embed.FS

func GenerateHTML(baseURL string) string {
	htmlPart, _ := frontendFiles.ReadFile("html_part.html")
	htmlsPart, _ := frontendFiles.ReadFile("htmls_part.html")

	htmlStr := string(htmlPart)
	htmlsStr := string(htmlsPart)

	// html_part ends with: <body style="margin-bottom: 80px;"><script>
	// htmls_part starts with: \n</script>\n  <div id="app">...
	htmlStr = strings.TrimSuffix(strings.TrimRight(htmlStr, " \t\r\n"), "<script>")
	htmlsStr = strings.TrimPrefix(strings.TrimLeft(htmlsStr, " \t\r\n"), "</script>")

	// Replace env placeholder: "${$.getEnv() || ''}" → "Server"
	htmlsStr = strings.Replace(htmlsStr, "${$.getEnv() || ''}", "Server", 1)

	// Inject the client-side conversion scripts so the frontend can do
	// in-browser conversion (frontendConvert), mirroring preview.js.
	htmlsStr = injectConvertScripts(htmlsStr)

	// Replace script.hub baseUrl with actual server URL
	if !strings.HasPrefix(baseURL, "http://script.hub") {
		htmlsStr = strings.Replace(htmlsStr,
			"baseUrl: 'http://script.hub/',",
			fmt.Sprintf("baseUrl: '%s/',", strings.TrimRight(baseURL, "/")),
			1)
	}

	// Assemble: html_part + CDN + htmls_part
	vueCDN := `<script src="https://unpkg.com/vue@3/dist/vue.global.js"></script>` + "\n"
	return htmlStr + vueCDN + htmlsStr
}

// scriptBundle is JSON-serialized into the "__SCRIPT__" placeholder.
type scriptBundle struct {
	ScriptConverter string `json:"scriptConverter"`
	RewriteParser   string `json:"rewriteParser"`
	RuleParser      string `json:"ruleParser"`
	ScriptMap       string `json:"scriptMap"`
}

// injectConvertScripts replaces the "__SCRIPT__" placeholder with the bundled
// conversion scripts and re-enables frontendConvert (server side embeds the
// scripts, so browser-side conversion is available).
func injectConvertScripts(htmlsStr string) string {
	rewriteParser, _ := convertScripts.ReadFile("scripts/Rewrite-Parser.js")
	ruleParser, _ := convertScripts.ReadFile("scripts/rule-parser.js")
	scriptConverter, _ := convertScripts.ReadFile("scripts/script-converter.js")
	scriptMap, _ := convertScripts.ReadFile("scripts/scriptMap.js")

	bundle := scriptBundle{
		ScriptConverter: string(scriptConverter),
		RewriteParser:   string(rewriteParser),
		RuleParser:      string(ruleParser),
		ScriptMap:       string(scriptMap),
	}
	jsonStr, _ := json.Marshal(bundle)
	htmlsStr = strings.Replace(htmlsStr, `"__SCRIPT__"`, string(jsonStr), 1)

	// Re-enable frontendConvert now that scripts are embedded.
	htmlsStr = strings.Replace(htmlsStr,
		"frontendConvertDisabled: function () {\n        return !/^Node\\.js/i.test(init.env)",
		"frontendConvertDisabled: function () {\n        return !/^Server/i.test(init.env)",
		1)

	return htmlsStr
}
