package rewrite

import (
	"strings"
	"testing"
)

func TestRejectVideoDropParsing(t *testing.T) {
	p := &Parser{}
	tests := []struct {
		in   string
		want RewriteType
	}{
		{"^video url reject-video", RewriteTypeRejectVideo},
		{"^drop url reject-drop", RewriteTypeRejectDrop},
	}
	for _, tt := range tests {
		mods := p.parseQXRewrite(tt.in, map[string]string{})
		if len(mods) == 0 || len(mods[0].Rewrites) == 0 || mods[0].Rewrites[0].Type != tt.want {
			t.Fatalf("parse %q: got %+v want %v", tt.in, mods, tt.want)
		}
	}
}

func TestRejectVideoSurgeMapsToTinyGif(t *testing.T) {
	p := &Parser{}
	mods := p.parseQXRewrite("^ads url reject-video", map[string]string{})
	out := p.convertToSurgeFormat(mods, "surge", map[string]string{})
	if !strings.Contains(out, "^ads reject-tinygif") {
		t.Fatalf("expected reject-tinygif mapping, got:\n%s", out)
	}
}

func TestPinPoutFiltering(t *testing.T) {
	p := &Parser{}
	content := "^keep url reject\n^ads url reject\n^other url reject"
	args := map[string]string{"x": "ads", "y": "keep"}
	mods := p.parseQXRewrite(content, args)
	patterns := map[string]bool{}
	for _, r := range mods[0].Rewrites {
		patterns[r.Pattern] = true
	}
	if patterns["^ads"] {
		t.Fatalf("ads should be excluded, got patterns: %v", patterns)
	}
	if !patterns["^keep"] {
		t.Fatalf("keep should be rescued, got patterns: %v", patterns)
	}
}

func TestDedupRewrites(t *testing.T) {
	rws := []ParsedRewrite{
		{Pattern: "^a", Type: RewriteTypeReject},
		{Pattern: "^a", Type: RewriteTypeReject},
		{Pattern: "^b", Type: RewriteTypeReject},
	}
	out := dedupRewrites(rws)
	if len(out) != 2 {
		t.Fatalf("dedup count: got %d want 2", len(out))
	}
}

func TestDedupScripts(t *testing.T) {
	rws := []ParsedRewrite{
		{ScriptType: "http-response", Pattern: "^a", ScriptPath: "p.js"},
		{ScriptType: "http-response", Pattern: "^a", ScriptPath: "p.js"},
		{ScriptType: "http-response", Pattern: "^a", ScriptPath: "q.js"},
	}
	out := dedupScripts(rws)
	if len(out) != 2 {
		t.Fatalf("dedup scripts count: got %d want 2", len(out))
	}
}

func TestApplySniPm(t *testing.T) {
	rules := []string{"DOMAIN,foo.com", "IP-CIDR,1.2.3.0/24", "DOMAIN,bar.com"}
	out := ApplySniPm(rules, "foo", "")
	if !strings.Contains(out[0], "extended-matching") {
		t.Fatalf("sni not applied to foo: %v", out)
	}
	if strings.Contains(out[1], "extended-matching") {
		t.Fatalf("sni wrongly applied to ip-cidr: %v", out)
	}
}

func TestParsePanelLine(t *testing.T) {
	p := parsePanelLine("MyPanel = title=Hello, style=info, script-name=panel.js, update-interval=60")
	if p == nil {
		t.Fatal("panel not parsed")
	}
	if p.Name != "MyPanel" || p.Title != "Hello" || p.Style != "info" || p.ScriptName != "panel.js" || p.UpdateTimer != "60" {
		t.Fatalf("panel fields wrong: %+v", p)
	}
}

func TestParseHostLine(t *testing.T) {
	h := parseHostLine("example.com = server:1.2.3.4")
	if h == nil {
		t.Fatal("host not parsed")
	}
	if h.Domain != "example.com" || h.Value != "server:1.2.3.4" {
		t.Fatalf("host fields wrong: %+v", h)
	}
}

func TestSurgePanelHostOutput(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{
		Panels: []PanelInfo{{Name: "P", Title: "T", ScriptName: "s.js"}},
		Hosts:  []HostInfo{{Domain: "a.com", Value: "server:1.1.1.1"}},
	}}
	out := p.convertToSurgeFormat(mods, "surge", map[string]string{})
	if !strings.Contains(out, "[Panel]") || !strings.Contains(out, "script-name=s.js") {
		t.Fatalf("panel section missing:\n%s", out)
	}
	if !strings.Contains(out, "[Host]") || !strings.Contains(out, "a.com = server:1.1.1.1") {
		t.Fatalf("host section missing:\n%s", out)
	}
}