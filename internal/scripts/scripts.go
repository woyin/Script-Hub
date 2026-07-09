// Package scripts 实现脚本辅助功能，对应 JS 版 scripts/ 目录下的工具脚本。
// 包括：请求头替换（replace-header）、回显响应（echo-response）、请求体替换（replace-body）。
package scripts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/script-hub-org/script-hub/internal/util"
)

// ── 请求头替换 ──
// 对应 JS 版 scripts/replace-header.js

// ReplaceHeaderInput 请求头替换的输入参数。
type ReplaceHeaderInput struct {
	Method  string            // HTTP 方法
	URL     string            // 请求 URL
	Headers map[string]string // 原始请求头
	Arg     string            // 替换规则参数
}

// ReplaceHeaderOutput 请求头替换的输出结果。
type ReplaceHeaderOutput struct {
	Status  int               // HTTP 状态码
	Headers map[string]string // 替换后的请求头
	Body    string            // 响应体（此处为空）
}

// ReplaceHeader 应用正则替换规则修改请求头。
// 参数格式："pattern1->replacement1&pattern2->replacement2"
func ReplaceHeader(input ReplaceHeaderInput) ReplaceHeaderOutput {
	replacements := parseArgReplacements(input.Arg)
	headers := input.Headers

	for _, rep := range replacements {
		re := getRegexp(rep.Pattern)
		for k, v := range headers {
			if re.MatchString(k + ": " + v) {
				newVal := re.ReplaceAllString(k+": "+v, rep.Replacement)
				parts := strings.SplitN(newVal, ": ", 2)
				if len(parts) == 2 {
					delete(headers, k)
					headers[parts[0]] = parts[1]
				}
			}
		}
	}

	return ReplaceHeaderOutput{
		Status:  200,
		Headers: headers,
		Body:    "",
	}
}

// ── 回显响应 ──
// 对应 JS 版 scripts/echo-response.js

// EchoResponseInput 回显响应的输入参数。
type EchoResponseInput struct {
	Arg     string            // 回显参数（type=...&url=...）
	URL     string            // 请求 URL
	Headers map[string]string // 请求头
	Content string            // 原始内容
}

// EchoResponseOutput 回显响应的输出结果。
type EchoResponseOutput struct {
	Status      int               // HTTP 状态码
	Headers     map[string]string // 响应头
	Body        string            // 响应体
	Redirect    bool              // 是否为重定向
	RedirectURL string            // 重定向目标 URL
}

// EchoResponse 处理 echo-response 类型重写。
// 支持：text/html 回显、URL 重定向、自定义状态码。
func EchoResponse(input EchoResponseInput) EchoResponseOutput {
	args := util.ParseQueryStringLenient(input.Arg)
	contentType := args["type"]
	echoURL := args["url"]
	statusCode := 200
	if sc := args["status-code"]; sc != "" {
		fmt.Sscanf(sc, "%d", &statusCode)
	}

	// 同时设置了 type 和 url：返回指定 Content-Type 的内容
	if contentType != "" && echoURL != "" {
		return EchoResponseOutput{
			Status:  statusCode,
			Headers: map[string]string{"Content-Type": contentType},
			Body:    input.Content,
		}
	}

	// 仅设置 url：302 重定向
	if echoURL != "" {
		return EchoResponseOutput{
			Status:      302,
			Redirect:    true,
			RedirectURL: echoURL,
		}
	}

	// text 参数：纯文本回显
	if text := args["text"]; text != "" {
		return EchoResponseOutput{
			Status:  statusCode,
			Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Body:    text,
		}
	}

	// 默认：回显原始内容
	return EchoResponseOutput{
		Status:  statusCode,
		Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		Body:    input.Content,
	}
}

// ── 请求体替换 ──
// 对应 JS 版 scripts/replace-body.js

// ReplaceBodyInput 请求体替换的输入参数。
type ReplaceBodyInput struct {
	Body string // 原始请求体
	Arg  string // 替换规则参数
}

// ReplaceBodyOutput 请求体替换的输出结果。
type ReplaceBodyOutput struct {
	Body string // 替换后的请求体
}

// ReplaceBody 应用正则替换规则修改请求体。
// 参数格式同 ReplaceHeader。
func ReplaceBody(input ReplaceBodyInput) ReplaceBodyOutput {
	body := input.Body
	replacements := parseArgReplacements(input.Arg)

	for _, rep := range replacements {
		re := getRegexp(rep.Pattern)
		body = re.ReplaceAllString(body, rep.Replacement)
	}

	return ReplaceBodyOutput{Body: body}
}

// ── 内部工具函数 ──

// replacement 表示一条替换规则。
type replacement struct {
	Pattern     string // 正则模式
	Replacement string // 替换字符串
}

// parseArgReplacements 解析替换规则参数。
// 格式："pattern1->replacement1&pattern2->replacement2"
func parseArgReplacements(arg string) []replacement {
	if arg == "" {
		return nil
	}
	var reps []replacement
	pairs := strings.Split(arg, "&")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "->", 2)
		if len(parts) == 2 {
			reps = append(reps, replacement{
				Pattern:     parts[0],
				Replacement: parts[1],
			})
		}
	}
	return reps
}

// getRegexp 编译正则表达式，支持 /pattern/flags 格式。
func getRegexp(pattern string) *regexp.Regexp {
	reParts := regexp.MustCompile(`^/(.*?)/([gims]*)$`).FindStringSubmatch(pattern)
	if len(reParts) > 2 {
		return regexp.MustCompile(reParts[1])
	}
	return regexp.MustCompile(pattern)
}
