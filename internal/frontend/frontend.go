// Input: embed, strings
// Output: func GenerateHTML()
// Pos: UI层-前端页面，内嵌 index.html 并注入 baseURL 生成转换页面
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package frontend provides the minimal conversion UI.
package frontend

import (
	_ "embed"
	"strings"
)

//go:embed index.html
var page string

// GenerateHTML returns the conversion page configured for this server.
func GenerateHTML(baseURL string) string {
	return strings.Replace(page, "__BASE_URL__", strings.TrimRight(baseURL, "/"), 1)
}
