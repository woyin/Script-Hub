// Input: os, strconv
// Output: type Platform, type Config, func LoadConfig(), 平台/目标格式常量
// Pos: 配置层-全局配置，从环境变量加载运行时参数并定义平台与格式常量
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package config 提供 Script Hub 的全局配置加载。
// 所有配置项均通过环境变量读取，与 JS 版 service.js / preview.js 的环境变量一一对应。
package config

import (
	"os"
	"strconv"
)

// Platform 表示目标代理平台类型
type Platform string

const (
	PlatformQX           Platform = "qx"
	PlatformSurge        Platform = "surge"
	PlatformLoon         Platform = "loon"
	PlatformStash        Platform = "stash"
	PlatformShadowrocket Platform = "shadowrocket"
	PlatformEgern        Platform = "egern"  // Egern 是 Surge 兼容客户端
	PlatformLanceX       Platform = "lancex" // LanceX 是 Surge 兼容客户端
)

// ── 重写转换的目标格式 ──
const (
	TargetSurgeModule         = "surge-module"          // Surge 模块 (.sgmodule)
	TargetStashStoverride     = "stash-stoverride"      // Stash 覆写 (.stoverride)
	TargetLoonPlugin          = "loon-plugin"           // Loon 插件 (.plugin)
	TargetShadowrocketModule  = "shadowrocket-module"   // Shadowrocket 模块 (.sgmodule)
	TargetEgernModule         = "egern-module"          // Egern 模块（Surge 兼容）
	TargetLanceXModule        = "lancex-module"         // LanceX 模块（Surge 兼容）
	TargetQXRewrite           = "qx-rewrite"            // Quantumult X 重写配置
	TargetSurgeRuleSet        = "surge-rule-set"        // Surge 规则集
	TargetStashRuleSet        = "stash-rule-set"        // Stash 规则集
	TargetLoonRuleSet         = "loon-rule-set"         // Loon 规则集
	TargetShadowrocketRuleSet = "shadowrocket-rule-set" // Shadowrocket 规则集
	TargetEgernRuleSet        = "egern-rule-set"        // Egern 规则集（Surge 兼容）
	TargetLanceXRuleSet       = "lancex-rule-set"       // LanceX 规则集（Surge 兼容）
	TargetSurgeDomainSet      = "surge-domain-set"      // Surge 域名集
	TargetSurgeDomainSet2     = "surge-domain-set2"     // 无法转换为域名集的剩余规则集
	TargetStashDomainSet      = "stash-domain-set"      // Stash 域名集
	TargetStashDomainSet2     = "stash-domain-set2"     // 无法转换为域名集的剩余规则集
)

// ── 重写解析的来源格式 ──
const (
	SourceTypeQXRewrite   = "qx-rewrite"   // QX 重写规则
	SourceTypeSurgeModule = "surge-module" // Surge 模块
	SourceTypeLoonPlugin  = "loon-plugin"  // Loon 插件
	SourceTypeAllModule   = "all-module"   // 所有模块格式（自动识别）
	SourceTypeRuleSet     = "rule-set"     // 规则集
)

// Config 保存所有运行时配置参数
type Config struct {
	Port        string // 监听端口（默认 9100）
	Host        string // 监听地址（默认 0.0.0.0）
	HTTPTimeout int    // HTTP 请求超时秒数（默认 20）
	MaxBodyKB   int    // 最大响应体 KB 数（默认 600）
}

// LoadConfig 从环境变量加载配置，未设置的使用默认值。
func LoadConfig() *Config {
	port := getEnv("PORT", "9100")
	host := getEnv("HOST", "0.0.0.0")
	httpTimeout := getEnvInt("HTTP_TIMEOUT", 20)

	return &Config{
		Port:        port,
		Host:        host,
		HTTPTimeout: httpTimeout,
		MaxBodyKB:   getEnvInt("PARSER_BODY_MAX", 600),
	}
}

// getEnv 读取环境变量，若为空则返回 fallback
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt 读取环境变量并解析为整数，失败则返回 fallback
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
