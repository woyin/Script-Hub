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
	PlatformEgern        Platform = "egern"   // Egern 是 Surge 兼容客户端
	PlatformLanceX       Platform = "lancex"  // LanceX 是 Surge 兼容客户端
)

// ── 重写转换的目标格式 ──
const (
	TargetSurgeModule         = "surge-module"          // Surge 模块 (.sgmodule)
	TargetStashStoverride     = "stash-stoverride"      // Stash 覆写 (.stoverride)
	TargetLoonPlugin          = "loon-plugin"           // Loon 插件 (.plugin)
	TargetShadowrocketModule  = "shadowrocket-module"   // Shadowrocket 模块 (.sgmodule)
	TargetSurgeRuleSet        = "surge-rule-set"         // Surge 规则集
	TargetStashRuleSet        = "stash-rule-set"         // Stash 规则集
	TargetLoonRuleSet         = "loon-rule-set"          // Loon 规则集
	TargetShadowrocketRuleSet = "shadowrocket-rule-set"  // Shadowrocket 规则集
	TargetSurgeDomainSet      = "surge-domain-set"       // Surge 域名集
	TargetSurgeDomainSet2     = "surge-domain-set2"      // 无法转换为域名集的剩余规则集
	TargetStashDomainSet      = "stash-domain-set"       // Stash 域名集
	TargetStashDomainSet2     = "stash-domain-set2"      // 无法转换为域名集的剩余规则集
)

// ── 重写解析的来源格式 ──
const (
	SourceTypeQXRewrite   = "qx-rewrite"    // QX 重写规则
	SourceTypeSurgeModule = "surge-module"  // Surge 模块
	SourceTypeLoonPlugin  = "loon-plugin"   // Loon 插件
	SourceTypeAllModule   = "all-module"    // 所有模块格式（自动识别）
	SourceTypeRuleSet     = "rule-set"      // 规则集
)

// Config 保存所有运行时配置参数
type Config struct {
	Port        string // 正式服务监听端口（默认 9100）
	BetaPort    string // Beta 服务监听端口（默认 9101）
	Host        string // 监听地址（默认 0.0.0.0）
	BaseURL     string // 正式服务对外 URL
	BetaBaseURL string // Beta 服务对外 URL
	HTTPTimeout int    // HTTP 请求超时秒数（默认 20）
	MaxBodyKB   int    // 最大响应体 KB 数（默认 600）
	ExportHTML  string // 静态 HTML 导出目录（为空则启动 HTTP 服务）
}

// LoadConfig 从环境变量加载配置，未设置的使用默认值。
// 默认值与 JS 版 service.js 完全对齐。
func LoadConfig() *Config {
	port := getEnv("PORT", "9100")
	betaPort := getEnv("BETA_PORT", "9101")
	host := getEnv("HOST", "0.0.0.0")
	baseURL := getEnv("BASE_URL", "http://127.0.0.1:"+port)
	betaBaseURL := getEnv("BETA_BASE_URL", "http://127.0.0.1:"+betaPort)
	// HTTP_TIMEOUT 对应 JS 版的 HTTP_TIMEOUT 环境变量（单位：秒）
	httpTimeout := getEnvInt("HTTP_TIMEOUT", 20)

	return &Config{
		Port:        port,
		BetaPort:    betaPort,
		Host:        host,
		BaseURL:     baseURL,
		BetaBaseURL: betaBaseURL,
		HTTPTimeout: httpTimeout,
		MaxBodyKB:   getEnvInt("PARSER_BODY_MAX", 600),
		ExportHTML:  os.Getenv("EXPORT_HTML"),
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
