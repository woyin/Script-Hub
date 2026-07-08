package frontend

import (
	"strings"
	"testing"
)

func TestGenerateHTMLInjectsScripts(t *testing.T) {
	html := GenerateHTML("http://127.0.0.1:9100")
	// __SCRIPT__ placeholder must be replaced with a real object (not {})
	if strings.Contains(html, `"__SCRIPT__"`) {
		t.Fatalf("__SCRIPT__ placeholder not replaced")
	}
	if strings.Contains(html, `= {}`) && strings.Contains(html, `scriptMap`) {
		// could be legit; check specifically that bundle fields are present
	}
	if !strings.Contains(html, "rewriteParser") {
		t.Fatalf("rewriteParser not injected into __SCRIPT__")
	}
	if !strings.Contains(html, "ruleParser") {
		t.Fatalf("ruleParser not injected into __SCRIPT__")
	}
}

func TestGenerateHTMLEnablesFrontendConvert(t *testing.T) {
	html := GenerateHTML("http://127.0.0.1:9100")
	// must NOT hardcode frontendConvertDisabled to return true
	if strings.Contains(html, "frontendConvertDisabled: function () {\n        return true") {
		t.Fatalf("frontendConvert should not be force-disabled")
	}
	// the env check should now accept 'Server' (injected env value)
	if !strings.Contains(html, "/^Server/i.test(init.env)") {
		t.Fatalf("frontendConvert env check not updated to Server:\n%s", html)
	}
}

func TestGenerateHTMLBaseUrl(t *testing.T) {
	html := GenerateHTML("http://example.com:8080")
	if !strings.Contains(html, "baseUrl: 'http://example.com:8080/'") {
		t.Fatalf("baseUrl not replaced:\n%s", html)
	}
}