// Package util 提供Script Hub各个模块共享的工具函数。
// 这些函数从重复的包内实现中提取而来，统一维护以确保行为一致。
package util

import (
	"net/url"
	"strings"
)

// IsTrue 判断字符串是否表示布尔"真"值。
// 兼容 JS 端的判断逻辑：接受 "true"、"1"、"True"。
func IsTrue(s string) bool {
	return s == "true" || s == "1" || s == "True"
}

// GetArgArr 将 "+" 分隔的参数字符串拆分为数组。
// 其中 "➕" 会被还原为真正的 "+" 字符，与 JS 端的 arg 解析行为一致。
// 例如："keyword1+keyword2" → ["keyword1", "keyword2"]
//       "a➕b+c" → ["a+b", "c"]
func GetArgArr(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "+")
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = strings.ReplaceAll(p, "➕", "+")
	}
	return result
}

// ParseQueryString 解析 URL 查询字符串为键值对 map。
// 行为与 JS 端 parseQueryString 对齐：
//   - 裸参数（无 "="）会被忽略（JS 端的正则要求有 "=" 才匹配）
//   - 使用 url.PathUnescape 而非 QueryUnescape，因为 JS 的 decodeURIComponent
//     不会将 "+" 解码为空格
//
// server/handler.go 使用此版本；scripts/scripts.go 中的简化版（允许裸参数）
// 保留为 ParseQueryStringLenient。
func ParseQueryString(query string) map[string]string {
	result := make(map[string]string)
	if query == "" {
		return result
	}
	// 镜像 JS 行为：取第一个 '?' 之后的部分
	if idx := strings.Index(query, "?"); idx >= 0 {
		query = query[idx+1:]
	} else {
		query = strings.TrimPrefix(query, "?")
	}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		// JS regex 要求 '='，所以裸参数（无 '='）被忽略
		key, _ := url.PathUnescape(kv[0])
		if len(kv) == 2 {
			val, _ := url.PathUnescape(kv[1])
			result[key] = val
		}
		_ = key
	}
	return result
}

// ParseQueryStringLenient 解析 URL 查询字符串为键值对 map（宽松模式）。
// 与 ParseQueryString 不同，裸参数（无 "="）会被保留为空值。
// 用于脚本参数解析等场景。
func ParseQueryStringLenient(query string) map[string]string {
	result := make(map[string]string)
	if query == "" {
		return result
	}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			result[kv[0]] = kv[1]
		} else {
			result[kv[0]] = ""
		}
	}
	return result
}
