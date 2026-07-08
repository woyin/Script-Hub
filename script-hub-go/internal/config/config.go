package config

import "os"

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
	TargetSurgeModule        = "surge-module"
	TargetStashStoverride    = "stash-stoverride"
	TargetLoonPlugin         = "loon-plugin"
	TargetShadowrocketModule = "shadowrocket-module"
	TargetSurgeRuleSet       = "surge-rule-set"
	TargetStashRuleSet       = "stash-rule-set"
	TargetLoonRuleSet        = "loon-rule-set"
	TargetShadowrocketRuleSet = "shadowrocket-rule-set"
	TargetSurgeDomainSet     = "surge-domain-set"
	TargetSurgeDomainSet2    = "surge-domain-set2"
	TargetStashDomainSet     = "stash-domain-set"
	TargetStashDomainSet2    = "stash-domain-set2"
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
	Host        string
	BaseURL     string
	HTTPTimeout int
	MaxBodyKB   int
}

func LoadConfig() *Config {
	port := getEnv("PORT", "9100")
	host := getEnv("HOST", "0.0.0.0")
	baseURL := getEnv("BASE_URL", "http://127.0.0.1:"+port)

	return &Config{
		Port:        port,
		Host:        host,
		BaseURL:     baseURL,
		HTTPTimeout: 20,
		MaxBodyKB:   600,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
