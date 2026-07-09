// Package frontend 实现 Script Hub 的 Web 前端生成。
// 通过 embed 嵌入 HTML 模板和 JS 脚本，在运行时组装为完整的 Vue.js 页面。
// 对应 JS 版 script-hub.js 中的 HTML 输出和 preview.js 中的静态导出逻辑。
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

// GenerateHTML 生成完整的 Script Hub 前端页面。
// 处理步骤：
//  1. 读取并拼接 html_part（头部）和 htmls_part（Vue 应用体）
//  2. 替换环境占位符：${$.getEnv() || ''} → "Server"
//  3. 注入转换脚本（Rewrite-Parser、rule-parser、script-converter、scriptMap）
//  4. 替换 baseUrl 占位符为实际服务地址
//  5. 插入 Vue CDN 引用
func GenerateHTML(baseURL string) string {
	htmlPart, _ := frontendFiles.ReadFile("html_part.html")
	htmlsPart, _ := frontendFiles.ReadFile("htmls_part.html")

	htmlStr := string(htmlPart)
	htmlsStr := string(htmlsPart)

	// html_part 以 <body style="margin-bottom: 80px;"><script> 结尾
	// htmls_part 以 \n</script>\n  <div id="app"> 开头
	htmlStr = strings.TrimSuffix(strings.TrimRight(htmlStr, " \t\r\n"), "<script>")
	htmlsStr = strings.TrimPrefix(strings.TrimLeft(htmlsStr, " \t\r\n"), "</script>")

	// 替换环境占位符：${$.getEnv() || ''} → "Server"
	htmlsStr = strings.Replace(htmlsStr, "${$.getEnv() || ''}", "Server", 1)

	// 注入转换脚本，使前端可以执行浏览器端转换（frontendConvert）
	htmlsStr = injectConvertScripts(htmlsStr)

	// 替换 script.hub baseUrl 为实际服务地址
	if !strings.HasPrefix(baseURL, "http://script.hub") {
		htmlsStr = strings.Replace(htmlsStr,
			"baseUrl: 'http://script.hub/',",
			fmt.Sprintf("baseUrl: '%s/',", strings.TrimRight(baseURL, "/")),
			1)
	}

	// 组装：html_part + Vue CDN + htmls_part
	vueCDN := `<script src="https://unpkg.com/vue@3/dist/vue.global.js"></script>` + "\n"
	return htmlStr + vueCDN + htmlsStr
}

// scriptBundle 是注入到前端 __SCRIPT__ 占位符的脚本集合。
// 对应 JS 版 preview.js 中的 scriptBundle JSON 序列化。
type scriptBundle struct {
	ScriptConverter string `json:"scriptConverter"` // script-converter.js 内容
	RewriteParser   string `json:"rewriteParser"`   // Rewrite-Parser.js 内容
	RuleParser      string `json:"ruleParser"`       // rule-parser.js 内容
	ScriptMap       string `json:"scriptMap"`         // scriptMap.js 内容
}

// injectConvertScripts 替换 "__SCRIPT__" 占位符为嵌入的转换脚本，
// 并启用 frontendConvert（服务端嵌入脚本后，浏览器端转换可用）。
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

	// 启用 frontendConvert：将 Node.js 环境检测改为 Server 环境检测
	htmlsStr = strings.Replace(htmlsStr,
		"frontendConvertDisabled: function () {\n        return !/^Node\\.js/i.test(init.env)",
		"frontendConvertDisabled: function () {\n        return !/^Server/i.test(init.env)",
		1)

	return htmlsStr
}
