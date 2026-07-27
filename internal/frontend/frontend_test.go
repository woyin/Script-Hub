package frontend

import (
	"strings"
	"testing"
)

func TestGenerateHTML(t *testing.T) {
	html := GenerateHTML("https://example.com/")
	if strings.Contains(html, "__BASE_URL__") || !strings.Contains(html, "const base='https://example.com'") {
		t.Fatal("base URL not injected")
	}
	for _, required := range []string{
		"/file/_start_/", "localtext", "http://local.text", "navigator.clipboard.writeText",
		"all-module", "qx-rewrite", "surge-module", "loon-plugin", "rule-set",
		"stash-stoverride", "shadowrocket-module", "surge-rule-set", "loon-rule-set",
		"stash-rule-set", "shadowrocket-rule-set", "surge-domain-set", "stash-domain-set",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("required UI contract missing: %s", required)
		}
	}
	for _, removed := range []string{"evalScript", "script-converter", "__SCRIPT__", "eval(", "scriptMap", "/reload", "Vue"} {
		if strings.Contains(html, removed) {
			t.Fatalf("removed runtime feature leaked into UI: %s", removed)
		}
	}
}
