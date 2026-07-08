package frontend

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed html_part.html htmls_part.html
var frontendFiles embed.FS

func GenerateHTML(baseURL string) string {
	htmlPart, _ := frontendFiles.ReadFile("html_part.html")
	htmlsPart, _ := frontendFiles.ReadFile("htmls_part.html")

	htmlStr := string(htmlPart)
	htmlsStr := string(htmlsPart)

	// html_part ends with: <body style="margin-bottom: 80px;"><script>
	// htmls_part starts with: \n</script>\n  <div id="app">...
	// Original assembly: html + inline_vue_runtime + htmls
	//   => <body...><script>[Vue runtime]</script>[app HTML + script]
	//
	// We replace: strip the <script> from html_part end and </script> from htmls_part start,
	// then insert CDN <script> properly after <body> tag.

	htmlStr = strings.TrimSuffix(strings.TrimRight(htmlStr, " \t\r\n"), "<script>")
	htmlsStr = strings.TrimPrefix(strings.TrimLeft(htmlsStr, " \t\r\n"), "</script>")

	// Replace env placeholder: "${$.getEnv() || ''}" → "Server"
	htmlsStr = strings.Replace(htmlsStr, "${$.getEnv() || ''}", "Server", 1)

	// Server mode: no inline scripts for client-side eval → stub __SCRIPT__ and disable frontendConvert
	htmlsStr = strings.Replace(htmlsStr, `"__SCRIPT__"`, `{}`, 1)
	htmlsStr = strings.Replace(htmlsStr,
		"frontendConvertDisabled: function () {\n        return !/^Node\\.js/i.test(init.env)",
		"frontendConvertDisabled: function () {\n        return true",
		1)

	// Replace script.hub baseUrl with actual server URL
	if !strings.HasPrefix(baseURL, "http://script.hub") {
		htmlsStr = strings.Replace(htmlsStr,
			"baseUrl: 'http://script.hub/',",
			fmt.Sprintf("baseUrl: '%s/',", strings.TrimRight(baseURL, "/")),
			1)
	}

	// Assemble: html_part (now ends with <body...>) + CDN + htmls_part (now starts with app HTML)
	vueCDN := `<script src="https://unpkg.com/vue@3/dist/vue.global.js"></script>` + "\n"
	return htmlStr + vueCDN + htmlsStr
}
