package rewrite

import (
	"context"
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

func TestCategoryMapping(t *testing.T) {
	// non-Loon: keyword → category
	mod := &ParsedModule{Keyword: "tools"}
	k, v := CategoryForOutput(mod, false)
	if k != "category" || v != "tools" {
		t.Fatalf("non-loon keyword mapping: got %s=%s", k, v)
	}
	// Loon: category → tag
	mod = &ParsedModule{Category: "tools"}
	k, v = CategoryForOutput(mod, true)
	if k != "tag" || v != "tools" {
		t.Fatalf("loon category mapping: got %s=%s", k, v)
	}
}

func TestCategoryOutput(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{Keyword: "tools"}}
	// Surge: keyword → category
	surgeOut := p.convertToSurgeFormat(mods, "surge", map[string]string{})
	if !strings.Contains(surgeOut, "#!category=tools") {
		t.Fatalf("surge category missing:\n%s", surgeOut)
	}
	// Loon: category → tag
	mods = []ParsedModule{{Category: "tools"}}
	loonOut := p.convertToLoonFormat(mods, "loon", map[string]string{})
	if !strings.Contains(loonOut, "#!tag=tools") {
		t.Fatalf("loon tag missing:\n%s", loonOut)
	}
}

func TestMetaExtraPassthrough(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{MetaExtra: []string{"#!button=a=b", "#!toggle=true"}}}
	out := p.convertToLoonFormat(mods, "loon", map[string]string{})
	if !strings.Contains(out, "#!button=a=b") || !strings.Contains(out, "#!toggle=true") {
		t.Fatalf("meta extra not preserved:\n%s", out)
	}
}

func TestApplyJsc(t *testing.T) {
	mk := func() []ParsedRewrite { return []ParsedRewrite{{ScriptPath: "https://example.com/foo.js"}} }

	out := ApplyJsc(mk(), "foo", "", "surge", "", false, "", "", "", "", "")
	if !strings.HasPrefix(out[0].ScriptPath, "http://script.hub/convert/_start_/https://example.com/foo.js") {
		t.Fatalf("jsc path not wrapped: %s", out[0].ScriptPath)
	}
	if !strings.Contains(out[0].ScriptPath, "target=surge-script") {
		t.Fatalf("jsc target missing: %s", out[0].ScriptPath)
	}
	if strings.Contains(out[0].ScriptPath, "wrap_response=true") {
		t.Fatalf("jsc should not add wrap_response: %s", out[0].ScriptPath)
	}
	// jsc2 adds wrap_response
	out = ApplyJsc(mk(), "", "foo", "surge", "", false, "", "", "", "", "")
	if !strings.Contains(out[0].ScriptPath, "wrap_response=true") {
		t.Fatalf("jsc2 wrap_response missing: %s", out[0].ScriptPath)
	}
	// non-matching keyword: unchanged
	out = ApplyJsc(mk(), "bar", "", "surge", "", false, "", "", "", "", "")
	if out[0].ScriptPath != "https://example.com/foo.js" {
		t.Fatalf("non-matching jsc should be unchanged: %s", out[0].ScriptPath)
	}
}

func TestRandomIconURL(t *testing.T) {
	u := randomIconURL("Doraemon(100P)")
	if !strings.HasPrefix(u, "https://github.com/Toperlock/Quantumult/raw/main/icon/Doraemon/Doraemon-") {
		t.Fatalf("random icon url wrong: %s", u)
	}
	if !strings.HasSuffix(u, ".png") {
		t.Fatalf("random icon should be png: %s", u)
	}
	// gif library
	u = randomIconURL("SomeGif(50P)")
	if !strings.HasSuffix(u, ".gif") {
		t.Fatalf("gif library should produce .gif: %s", u)
	}
}

func TestApplyIconReplacementBareName(t *testing.T) {
	mod := &ParsedModule{Icon: "color"}
	ApplyIconReplacement(context.Background(), mod, map[string]string{}, nil, false)
	// Without network, lookup returns "" so icon stays as-is (bare name)
	if mod.Icon != "color" && mod.Icon != "" {
		t.Fatalf("bare icon name without network should stay: %s", mod.Icon)
	}
}

func TestParseArgumentsLine(t *testing.T) {
	args := parseArgumentsLine("#!arguments=token:abc123, debug:true")
	if len(args) < 2 {
		t.Fatalf("expected 2 args, got %d: %+v", len(args), args)
	}
	if args[0].Key != "token" || args[0].Value != "abc123" {
		t.Fatalf("arg0 wrong: %+v", args[0])
	}
	if args[1].Key != "debug" || args[1].Type != "switch" {
		t.Fatalf("arg1 wrong: %+v", args[1])
	}
}

func TestApplyArgumentsTemplate(t *testing.T) {
	sgArg := []SgArgument{{Key: "token", Value: "abc"}}
	body := "var t = '{token}'"
	// Surge: {token} → {{{token}}}
	out := applyArgumentsTemplate(body, sgArg, "surge")
	if !strings.Contains(out, "{{{token}}}") {
		t.Fatalf("surge template wrong: %s", out)
	}
	// Stash: {token} → abc
	out = applyArgumentsTemplate(body, sgArg, "stash")
	if !strings.Contains(out, "'abc'") {
		t.Fatalf("stash template wrong: %s", out)
	}
}

func TestSurgeArgumentsOutput(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{SgArg: []SgArgument{{Key: "token", Value: "abc"}, {Key: "debug", Value: "true"}}}}
	out := p.convertToSurgeFormat(mods, "surge", map[string]string{})
	if !strings.Contains(out, "#!arguments=token:abc,debug:true") {
		t.Fatalf("surge arguments metadata missing:\n%s", out)
	}
}

func TestParseMockLine(t *testing.T) {
	// Surge Map Local form
	rw := parseMockLine(`^api\.example\.com data-type=text data="hello" status-code=200 header="Content-Type:text/plain"`)
	if rw == nil || rw.Type != RewriteTypeMock {
		t.Fatalf("mock not parsed: %+v", rw)
	}
	if rw.MockType != "text" || rw.MockData != "hello" || rw.MockStatus != "200" {
		t.Fatalf("mock fields wrong: %+v", rw)
	}
	// echo-response form
	rw = parseMockLine("^api\\.example\\.com url echo-response text/plain https://example.com/data")
	if rw == nil || rw.Type != RewriteTypeEchoResponse {
		t.Fatalf("echo-response not parsed: %+v", rw)
	}
	if rw.EchoCT != "text/plain" || rw.EchoURL != "https://example.com/data" {
		t.Fatalf("echo fields wrong: %+v", rw)
	}
}

func TestRejectDictMapLocal(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{Rewrites: []ParsedRewrite{{Pattern: "^api", Type: RewriteTypeRejectDict}}}}
	out := p.convertToSurgeFormat(mods, "surge", map[string]string{})
	if !strings.Contains(out, "[Map Local]") || !strings.Contains(out, `data="{}"`) {
		t.Fatalf("reject-dict map local missing:\n%s", out)
	}
}

func TestLoonMockResponseBody(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{Rewrites: []ParsedRewrite{{
		Pattern:  "^api",
		Type:     RewriteTypeMock,
		MockType: "json",
		MockIsLoon: true,
	}}}}
	out := p.convertToLoonFormat(mods, "loon", map[string]string{})
	if !strings.Contains(out, "mock-response-body") || !strings.Contains(out, "data-type=json") {
		t.Fatalf("loon mock-response-body missing:\n%s", out)
	}
}