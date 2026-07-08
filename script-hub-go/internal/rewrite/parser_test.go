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