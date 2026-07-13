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
	if strings.Contains(html, "evalScript") || strings.Contains(html, "script-converter") {
		t.Fatal("removed runtime features leaked into UI")
	}
}
