package config

import (
	"os"
	"testing"
)

// setEnv helper：在测试中设置环境变量并在 t.Cleanup 中恢复。
func setEnv(t *testing.T, k, v string) {
	t.Helper()
	old, ok := os.LookupEnv(k)
	os.Setenv(k, v)
	t.Cleanup(func() {
		if ok {
			os.Setenv(k, old)
		} else {
			os.Unsetenv(k)
		}
	})
}

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, ok := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() { // 注意：闭包捕获 k/old/ok，需在循环内立即调用以固定值
			if ok {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	clearEnv(t, "PORT", "HOST", "HTTP_TIMEOUT", "PARSER_BODY_MAX")
	cfg := LoadConfig()
	if cfg.Port != "9100" {
		t.Errorf("Port = %q, want 9100", cfg.Port)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want 0.0.0.0", cfg.Host)
	}
	if cfg.HTTPTimeout != 20 {
		t.Errorf("HTTPTimeout = %d, want 20", cfg.HTTPTimeout)
	}
	if cfg.MaxBodyKB != 600 {
		t.Errorf("MaxBodyKB = %d, want 600", cfg.MaxBodyKB)
	}
}

func TestLoadConfig_Override(t *testing.T) {
	setEnv(t, "PORT", "8080")
	setEnv(t, "HOST", "127.0.0.1")
	setEnv(t, "HTTP_TIMEOUT", "45")
	setEnv(t, "PARSER_BODY_MAX", "1024")
	cfg := LoadConfig()
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", cfg.Host)
	}
	if cfg.HTTPTimeout != 45 {
		t.Errorf("HTTPTimeout = %d, want 45", cfg.HTTPTimeout)
	}
	if cfg.MaxBodyKB != 1024 {
		t.Errorf("MaxBodyKB = %d, want 1024", cfg.MaxBodyKB)
	}
}

func TestLoadConfig_InvalidInt(t *testing.T) {
	setEnv(t, "HTTP_TIMEOUT", "not-a-number")
	setEnv(t, "PARSER_BODY_MAX", "")
	cfg := LoadConfig()
	if cfg.HTTPTimeout != 20 {
		t.Errorf("HTTPTimeout invalid → want fallback 20, got %d", cfg.HTTPTimeout)
	}
	if cfg.MaxBodyKB != 600 {
		t.Errorf("MaxBodyKB empty → want fallback 600, got %d", cfg.MaxBodyKB)
	}
}

// 常量完整性检查：防止未来手抖改坏字符串值。
func TestConstants(t *testing.T) {
	if SourceTypeQXRewrite != "qx-rewrite" {
		t.Errorf("SourceTypeQXRewrite = %q", SourceTypeQXRewrite)
	}
	if TargetSurgeModule != "surge-module" {
		t.Errorf("TargetSurgeModule = %q", TargetSurgeModule)
	}
	if PlatformSurge != "surge" {
		t.Errorf("PlatformSurge = %q", PlatformSurge)
	}
	// Egern/LanceX 应与 Surge 兼容客户端标识一致
	if PlatformEgern != "egern" {
		t.Errorf("PlatformEgern = %q", PlatformEgern)
	}
	if PlatformLanceX != "lancex" {
		t.Errorf("PlatformLanceX = %q", PlatformLanceX)
	}
}

func TestSetVersion(t *testing.T) {
	// Save and restore the global Version.
	old := Version
	t.Cleanup(func() { Version = old })

	// SetVersion should update the global, and LoadConfig should reflect it.
	SetVersion("v9.9.9")
	if Version != "v9.9.9" {
		t.Errorf("Version = %q, want v9.9.9", Version)
	}
	cfg := LoadConfig()
	if cfg.Version != "v9.9.9" {
		t.Errorf("cfg.Version = %q, want v9.9.9", cfg.Version)
	}

	// Empty value should not overwrite the existing version.
	SetVersion("")
	if Version != "v9.9.9" {
		t.Errorf("SetVersion(\"\") should be a no-op, but Version = %q", Version)
	}
}
