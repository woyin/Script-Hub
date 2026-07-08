package config

import (
	"os"
	"strconv"
)

type Platform string

const (
	PlatformQX           Platform = "qx"
	PlatformSurge        Platform = "surge"
	PlatformLoon         Platform = "loon"
	PlatformStash        Platform = "stash"
	PlatformShadowrocket Platform = "shadowrocket"
	PlatformEgern        Platform = "egern"
	PlatformLanceX       Platform = "lancex"
)

const (
	TargetSurgeModule         = "surge-module"
	TargetStashStoverride     = "stash-stoverride"
	TargetLoonPlugin          = "loon-plugin"
	TargetShadowrocketModule  = "shadowrocket-module"
	TargetSurgeRuleSet        = "surge-rule-set"
	TargetStashRuleSet        = "stash-rule-set"
	TargetLoonRuleSet         = "loon-rule-set"
	TargetShadowrocketRuleSet = "shadowrocket-rule-set"
	TargetSurgeDomainSet      = "surge-domain-set"
	TargetSurgeDomainSet2     = "surge-domain-set2"
	TargetStashDomainSet      = "stash-domain-set"
	TargetStashDomainSet2     = "stash-domain-set2"
)

const (
	SourceTypeQXRewrite   = "qx-rewrite"
	SourceTypeSurgeModule = "surge-module"
	SourceTypeLoonPlugin  = "loon-plugin"
	SourceTypeAllModule   = "all-module"
	SourceTypeRuleSet     = "rule-set"
)

type Config struct {
	Port        string
	BetaPort    string
	Host        string
	BaseURL     string
	BetaBaseURL string
	HTTPTimeout int
	MaxBodyKB   int
	ExportHTML  string
}

func LoadConfig() *Config {
	port := getEnv("PORT", "9100")
	betaPort := getEnv("BETA_PORT", "9101")
	host := getEnv("HOST", "0.0.0.0")
	baseURL := getEnv("BASE_URL", "http://127.0.0.1:"+port)
	betaBaseURL := getEnv("BETA_BASE_URL", "http://127.0.0.1:"+betaPort)
	// HTTP_TIMEOUT mirrors the JS HTTP_TIMEOUT env var (seconds). Dockerfile documents it.
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

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
