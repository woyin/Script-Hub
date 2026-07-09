// preview.go 提供预览 HTML 生成功能。
// 对应 JS 版 preview.js 中的 generateHTML 函数。
package server

import "github.com/script-hub-org/script-hub/internal/frontend"

// generatePreviewHTML 生成预览页面 HTML。
func generatePreviewHTML(baseURL string) string {
	return frontend.GenerateHTML(baseURL)
}
