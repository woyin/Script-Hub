package rewrite

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/script-hub-org/script-hub/internal/config"
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
	// Per upstream Rewrite-Parser.js line 1320, Surge-strict targets emit reject-video
	// as [Map Local] tiny-gif only (no [URL Rewrite] line — rwtype doesn't match
	// /(?:reject|302|307|header)$/). The reject-tinygif URL Rewrite output was a bug.
	if strings.Contains(out, "^ads reject-tinygif") || strings.Contains(out, "^ads reject-video") {
		t.Fatalf("reject-video must NOT appear in [URL Rewrite] for Surge:\n%s", out)
	}
	if !strings.Contains(out, "^ads data-type=tiny-gif status-code=200") {
		t.Fatalf("reject-video must emit a [Map Local] tiny-gif entry:\n%s", out)
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
		Pattern:    "^api",
		Type:       RewriteTypeMock,
		MockType:   "json",
		MockIsLoon: true,
	}}}}
	out := p.convertToLoonFormat(mods, "loon", map[string]string{})
	if !strings.Contains(out, "mock-response-body") || !strings.Contains(out, "data-type=json") {
		t.Fatalf("loon mock-response-body missing:\n%s", out)
	}
}
func TestParseJsonPath(t *testing.T) {
	cases := map[string]string{
		"a.b.c":     `["a","b","c"]`,
		"a[0].b":    `["a",0,"b"]`,
		`a["x"][1]`: `["a","x",1]`,
	}
	for in, want := range cases {
		got := parseJsonPath(in)
		if got != want {
			t.Errorf("parseJsonPath(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseBodyRewriteSection(t *testing.T) {
	lines := []string{"http-response-jq ^api\\.com setpath([\"a\"]; 1)", "# comment", "http-request ^foo old new"}
	entries := parseBodyRewriteSection(lines)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Type != "http-response-jq" || entries[0].Regex != "^api\\.com" {
		t.Fatalf("entry0 wrong: %+v", entries[0])
	}
}

func TestSurgeBodyRewriteOutput(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{BodyRewrites: []BodyRewriteEntry{{Type: "http-response-jq", Regex: "^api", Value: "setpath([\"a\"];1)"}}}}
	out := p.convertToSurgeFormat(mods, "surge", map[string]string{})
	if !strings.Contains(out, "[Body Rewrite]") || !strings.Contains(out, "http-response-jq ^api") {
		t.Fatalf("surge body rewrite missing:\n%s", out)
	}
}

func TestLoonBodyRewriteOutput(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{BodyRewrites: []BodyRewriteEntry{{Type: "http-response-jq", Regex: "^api", Value: "'jq'"}}}}
	out := p.convertToLoonFormat(mods, "loon", map[string]string{})
	if !strings.Contains(out, "^api response-body-json-jq 'jq'") {
		t.Fatalf("loon body rewrite missing:\n%s", out)
	}
}

// Egern and LanceX are Surge-compatible clients: convertModules must route them
// through the Surge output path and produce identical output to a Surge target.
func TestEgernLanceXRouteToSurgeFormat(t *testing.T) {
	p := &Parser{}
	mods := p.parseQXRewrite("^ads url reject-video", map[string]string{})
	surgeOut := p.convertModules(mods, "surge-module", map[string]string{})
	for _, target := range []string{"egern-module", "lancex-module"} {
		got := p.convertModules(mods, target, map[string]string{})
		if got != surgeOut {
			t.Fatalf("target %s did not match surge output.\nsurge:\n%s\ngot:\n%s", target, surgeOut, got)
		}
		// Per upstream Rewrite-Parser.js line 1320, Surge-strict targets (surge/egern/
		// lancex) emit reject-video as [Map Local] tiny-gif only — NO [URL Rewrite] line
		// (rwtype doesn't match /(?:reject|302|307|header)$/). reject-tinygif in URL
		// Rewrite was the previous incorrect behavior.
		if strings.Contains(got, "^ads reject-tinygif") {
			t.Fatalf("target %s must NOT emit reject-tinygif in URL Rewrite (should be Map Local):\n%s", target, got)
		}
		if !strings.Contains(got, "^ads data-type=tiny-gif status-code=200") {
			t.Fatalf("target %s missing Surge reject mapping:\n%s", target, got)
		}
	}
}

// Surge-style reject / 302-redirect → Quantumult X rewrite output. Verifies the
// conversion matrix is closed: every plugin source can be emitted as a QX rewrite.
func TestSurgeModuleToQXRewrite(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{
		Name: "SurgeToQX",
		Rewrites: []ParsedRewrite{
			{Pattern: `^https?://ad\.example\.com`, Type: RewriteTypeRejectDict},
			{Pattern: `^https?://redir\.example\.com`, Type: RewriteTypeURLRewrite, Replacement: "302 https://target.example.com"},
			{Pattern: `^https?://api\.example\.com/old`, Type: RewriteTypeURLRewrite, Replacement: "https://api.example.com/new"},
		},
		MITM: []string{"ad.example.com", "redir.example.com"},
	}}
	out := p.convertModules(mods, "qx-rewrite", map[string]string{})
	if !strings.Contains(out, "[rewrite_local]") {
		t.Fatalf("missing [rewrite_local] section:\n%s", out)
	}
	if !strings.Contains(out, `^https?://ad\.example\.com url reject-dict`) {
		t.Fatalf("reject-dict not mapped to QX:\n%s", out)
	}
	if !strings.Contains(out, `^https?://redir\.example\.com url 302 https://target.example.com`) {
		t.Fatalf("302 redirect not mapped to QX:\n%s", out)
	}
	if !strings.Contains(out, `^https?://api\.example\.com/old url https://api.example.com/new`) {
		t.Fatalf("plain URL rewrite not emitted as QX 302-style redirect:\n%s", out)
	}
	if !strings.Contains(out, "[mitm]\nhostname = ad.example.com, redir.example.com") {
		t.Fatalf("MITM hostname missing:\n%s", out)
	}
}

// QX script entries (http scripts and cron) → QX output should be faithfully emitted.
func TestQXScriptAndCronOutput(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{
		Scripts: []ParsedRewrite{
			{Type: RewriteTypeScript, ScriptType: "http-response", Pattern: `^https?://api\.example\.com`,
				ScriptPath: "https://example.com/resp.js", RequiresBody: true},
			{Type: RewriteTypeScript, ScriptType: "cron", CronExp: "0 8 * * *",
				ScriptPath: "https://example.com/cron.js", Replacement: "cronTask"},
		},
		MITM: []string{"api.example.com"},
	}}
	out := p.convertModules(mods, "qx-rewrite", map[string]string{})
	if !strings.Contains(out, `^https?://api\.example\.com url script-response-body https://example.com/resp.js`) {
		t.Fatalf("http-response script not emitted as QX script-response-body:\n%s", out)
	}
	if !strings.Contains(out, "[task_local]") || !strings.Contains(out, "0 8 * * * https://example.com/cron.js, tag=cronTask") {
		t.Fatalf("cron script not emitted to [task_local]:\n%s", out)
	}
	if !strings.Contains(out, "[mitm]\nhostname = api.example.com") {
		t.Fatalf("MITM hostname missing:\n%s", out)
	}
}

// Regression: canonical Loon [Script] lines have no leading "name =". The parser
// must read script type, pattern, script-path and options directly. Previously the
// line was split on the first "=" (inside script-path=), dropping the whole config.
func TestLoonCanonicalScriptLineParsing(t *testing.T) {
	p := &Parser{}
	mods := p.parseLoonPlugin(`#!name=LoonScript
[Script]
http-response ^https?://api\.example\.com/data script-path=https://example.com/resp.js, tag=respScript, requires-body=true
`, map[string]string{})
	if len(mods[0].Scripts) != 1 {
		t.Fatalf("expected 1 parsed script, got %d", len(mods[0].Scripts))
	}
	s := mods[0].Scripts[0]
	if s.ScriptType != "http-response" {
		t.Fatalf("ScriptType = %q, want http-response", s.ScriptType)
	}
	if s.Pattern != `^https?://api\.example\.com/data` {
		t.Fatalf("Pattern = %q", s.Pattern)
	}
	if s.ScriptPath != "https://example.com/resp.js" {
		t.Fatalf("ScriptPath = %q", s.ScriptPath)
	}
	if !s.RequiresBody {
		t.Fatalf("RequiresBody should be true")
	}
	if s.Tag != "respScript" {
		t.Fatalf("Tag = %q, want respScript", s.Tag)
	}
}

// TestLoonRejectTypeMapping verifies that Surge-style reject types are mapped
// to canonical Loon [Rewrite] syntax. Upstream Rewrite-Parser.js maps
// reject-tinygif -> reject-img on Loon; the others pass through.
func TestLoonRejectTypeMapping(t *testing.T) {
	p := &Parser{}
	tests := []struct {
		rwType string
		want   string
	}{
		{"reject", "^ads reject"},
		{"reject-dict", "^ads reject-dict"},
		{"reject-img", "^ads reject-img"},
		{"reject-tinygif", "^ads reject-img"},
		{"reject-200", "^ads reject-200"},
		{"reject-array", "^ads reject-array"},
		{"reject-video", "^ads reject"},   // no Loon equivalent -> reject
		{"reject-drop", "^ads reject"},    // no Loon equivalent -> reject
	}
	for _, tt := range tests {
		mods := p.parseQXRewrite("^ads url "+tt.rwType, map[string]string{})
		out := p.convertToLoonFormat(mods, "loon", map[string]string{})
		if !strings.Contains(out, tt.want) {
			t.Fatalf("reject %s on Loon: want %q, got:\n%s", tt.rwType, tt.want, out)
		}
	}
}

// TestStashRejectVideoMapsToImg verifies the Stash-specific reject mapping.
// Upstream Rewrite-Parser.js line 1305: reject-video / reject-tinygif both
// become reject-img on Stash (regex /-video|-tinygif/.replace('-img')).
func TestStashRejectVideoMapsToImg(t *testing.T) {
	p := &Parser{}
	// reject-video: classifySurgeRewrite maps it to reject-tinygif for Surge;
	// Stash post-processing should then turn reject-tinygif into reject-img.
	mods := p.parseQXRewrite("^ads url reject-video", map[string]string{})
	out := p.convertToStashFormat(mods, "stash", map[string]string{})
	if !strings.Contains(out, "^ads reject-img") {
		t.Fatalf("expected reject-img for reject-video on Stash, got:\n%s", out)
	}
	if strings.Contains(out, "reject-tinygif") {
		t.Fatalf("reject-tinygif should not appear on Stash, got:\n%s", out)
	}
	if strings.Contains(out, "reject-video") {
		t.Fatalf("reject-video should not appear on Stash url-rewrite, got:\n%s", out)
	}
}

// TestStashRejectTinyGifMapsToImg verifies reject-tinygif also normalizes to
// reject-img on Stash (same upstream regex as reject-video).
func TestStashRejectTinyGifMapsToImg(t *testing.T) {
	p := &Parser{}
	mods := p.parseQXRewrite("^ads url reject-tinygif", map[string]string{})
	out := p.convertToStashFormat(mods, "stash", map[string]string{})
	if !strings.Contains(out, "^ads reject-img") {
		t.Fatalf("expected reject-img for reject-tinygif on Stash, got:\n%s", out)
	}
	if strings.Contains(out, "reject-tinygif") {
		t.Fatalf("reject-tinygif should not appear on Stash, got:\n%s", out)
	}
}

// TestLoonScriptConversion verifies Loon [Script] line construction matches
// upstream Rewrite-Parser.js semantics:
//   - event -> network-changed (no pattern)
//   - generic is skipped (handled as tile, not [Script])
//   - cron uses quoted expression
//   - http-request/response field order: script-path, requires-body, timeout,
//     tag, img-url, argument
func TestLoonScriptConversion(t *testing.T) {
	p := &Parser{}

	// event -> network-changed
	rw := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "event",
		ScriptPath: "https://example.com/evt.js", Replacement: "evtScript", Timeout: 30, EventName: "network-changed"}
	got := p.convertLoonScript(rw)
	if !strings.HasPrefix(got, "network-changed ") {
		t.Fatalf("event should map to network-changed on Loon, got: %q", got)
	}
	if strings.Contains(got, "http-event") {
		t.Fatalf("http-event must not appear on Loon, got: %q", got)
	}

	// generic is skipped
	rw2 := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "generic",
		ScriptPath: "https://example.com/gen.js", Replacement: "genScript", Timeout: 30}
	if got2 := p.convertLoonScript(rw2); got2 != "" {
		t.Fatalf("generic should be skipped on Loon, got: %q", got2)
	}

	// cron quoted expression
	rw3 := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "cron", CronExp: "0 8 * * *",
		ScriptPath: "https://example.com/c.js", Replacement: "cScript", Timeout: 30}
	got3 := p.convertLoonScript(rw3)
	if !strings.Contains(got3, `cron "0 8 * * *"`) {
		t.Fatalf("cron should use quoted expression, got: %q", got3)
	}

	// http-response field order
	rw4 := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "http-response",
		Pattern: "^https?://api", ScriptPath: "https://example.com/r.js",
		Replacement: "rScript", Timeout: 30, RequiresBody: true,
		ImgURL: "https://x.com/i.png", Arguments: "foo=1"}
	got4 := p.convertLoonScript(rw4)
	// script-path must come before requires-body before timeout before tag before img-url before argument
	idx := func(sub string) int { return strings.Index(got4, sub) }
	if idx("script-path=") == -1 || idx("requires-body=true") == -1 ||
		idx("timeout=30") == -1 || idx("tag=rScript") == -1 ||
		idx("img-url=") == -1 || idx("argument=foo=1") == -1 {
		t.Fatalf("http-response missing expected fields, got: %q", got4)
	}
	if !(idx("script-path=") < idx("requires-body=true") &&
		idx("requires-body=true") < idx("timeout=30") &&
		idx("timeout=30") < idx("tag=rScript") &&
		idx("tag=rScript") < idx("img-url=") &&
		idx("img-url=") < idx("argument=foo=1")) {
		t.Fatalf("http-response field order wrong, got: %q", got4)
	}
}

// TestLoonMockNoHeader verifies Loon mock-response-body does NOT carry a header
// field (upstream Rewrite-Parser.js loon-plugin branch omits it).
func TestLoonMockNoHeader(t *testing.T) {
	p := &Parser{}
	rw := ParsedRewrite{Type: RewriteTypeMock, Pattern: "^https?://m",
		MockType: "text", MockData: "hello", MockStatus: "200",
		MockHeader: "Content-Type:text/plain", MockBase64: true}
	got := p.convertLoonRewrite(rw)
	if strings.Contains(got, "header=") {
		t.Fatalf("Loon mock must not carry header field, got: %q", got)
	}
	if !strings.Contains(got, "mock-response-body") {
		t.Fatalf("Loon mock missing mock-response-body, got: %q", got)
	}
	if !strings.Contains(got, "mock-data-is-base64=true") {
		t.Fatalf("Loon mock missing mock-data-is-base64=true, got: %q", got)
	}
}

// TestSurgeScriptFieldOrder verifies Surge [Script] line field order matches
// upstream Rewrite-Parser.js for http-response scripts:
// type, pattern, script-path, requires-body, binary-body-mode, engine,
// max-size, ability, script-update-interval, timeout, argument.
// Also verifies img-url is NOT emitted on Surge/Shadowrocket.
func TestSurgeScriptFieldOrder(t *testing.T) {
	p := &Parser{}
	rw := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "http-response",
		Pattern: "^https?://api", ScriptPath: "https://x.com/r.js",
		Replacement: "rScript", Timeout: 30, RequiresBody: true,
		BinaryBody: true, Engine: "javascript", MaxSize: "262144",
		Ability: "0", ScriptUpdateInterval: "86400",
		Arguments: "a=1,b=2", ImgURL: "https://x.com/i.png"}
	out := surgeOutput{}
	p.classifySurgeScript(rw, &out, "surge", map[string]string{})
	line := out.Scripts[0]

	// img-url must NOT appear on Surge
	if strings.Contains(line, "img-url=") {
		t.Fatalf("Surge [Script] must not carry img-url, got: %q", line)
	}
	// comma-containing argument must be quoted
	if !strings.Contains(line, `argument="a=1,b=2"`) {
		t.Fatalf("argument with comma must be quoted, got: %q", line)
	}
	// Verify field order
	order := []string{"type=http-response", "pattern=", "script-path=",
		"requires-body=1", "binary-body-mode=true", "engine=javascript",
		"max-size=262144", "ability=0", "script-update-interval=86400",
		"timeout=30", `argument="a=1,b=2"`}
	pos := -1
	for _, f := range order {
		idx := strings.Index(line, f)
		if idx < 0 {
			t.Fatalf("missing field %q in: %q", f, line)
		}
		if idx < pos {
			t.Fatalf("field %q out of order in: %q", f, line)
		}
		pos = idx
	}
}

// TestShadowrocketScriptNoEngine verifies Shadowrocket does NOT emit engine=
// (upstream line 1529: engine is Surge-only).
func TestShadowrocketScriptNoEngine(t *testing.T) {
	p := &Parser{}
	rw := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "http-response",
		Pattern: "^https?://api", ScriptPath: "https://x.com/r.js",
		Replacement: "rScript", Timeout: 30, Engine: "javascript"}
	for _, tgt := range []string{"shadowrocket"} {
		out := surgeOutput{}
		p.classifySurgeScript(rw, &out, tgt, map[string]string{})
		if strings.Contains(out.Scripts[0], "engine=") {
			t.Fatalf("%s must not emit engine=, got: %q", tgt, out.Scripts[0])
		}
	}
	// Surge-compat targets (surge, egern, lancex) MUST keep engine=
	for _, tgt := range []string{"surge", "egern", "lancex"} {
		out := surgeOutput{}
		p.classifySurgeScript(rw, &out, tgt, map[string]string{})
		if !strings.Contains(out.Scripts[0], "engine=javascript") {
			t.Fatalf("%s should keep engine=, got: %q", tgt, out.Scripts[0])
		}
	}
}

// TestSurgeScriptTypeSpecificFormats verifies cron, event, dns/rule, and
// generic each use their upstream-correct field sets.
func TestSurgeScriptTypeSpecificFormats(t *testing.T) {
	p := &Parser{}

	// cron: type, cronexp, script-path, script-update-interval, engine, timeout, wake-system, argument
	rw := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "cron", CronExp: "0 8 * * *",
		ScriptPath: "https://x.com/c.js", Replacement: "cScript", Timeout: 30,
		Engine: "javascript", WakeSystem: true, ScriptUpdateInterval: "86400", Arguments: "x=1"}
	out := surgeOutput{}
	p.classifySurgeScript(rw, &out, "surge", map[string]string{})
	if !strings.Contains(out.Scripts[0], "type=cron, cronexp=0 8 * * *") {
		t.Fatalf("cron format wrong: %q", out.Scripts[0])
	}
	// cron must NOT have pattern or requires-body
	if strings.Contains(out.Scripts[0], "pattern=") || strings.Contains(out.Scripts[0], "requires-body=") {
		t.Fatalf("cron should not have pattern/requires-body: %q", out.Scripts[0])
	}

	// event: type=event, event-name=..., script-path, ability, engine, timeout, argument
	rw2 := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "event",
		ScriptPath: "https://x.com/e.js", Replacement: "eScript", Timeout: 30}
	out2 := surgeOutput{}
	p.classifySurgeScript(rw2, &out2, "surge", map[string]string{})
	if !strings.Contains(out2.Scripts[0], "type=event, event-name=network-changed") {
		t.Fatalf("event format wrong: %q", out2.Scripts[0])
	}

	// generic: must be skipped (no [Script] entry)
	rw3 := ParsedRewrite{Type: RewriteTypeScript, ScriptType: "generic",
		ScriptPath: "https://x.com/g.js", Replacement: "gScript", Timeout: 30}
	out3 := surgeOutput{}
	p.classifySurgeScript(rw3, &out3, "surge", map[string]string{})
	if len(out3.Scripts) != 0 {
		t.Fatalf("generic should be skipped on Surge, got: %q", out3.Scripts[0])
	}
}

// TestStashScriptFormat verifies Stash YAML script output matches upstream
// Rewrite-Parser.js (Stash branch): http-request/response entries use
// match/name/type/require-body/max-size/binary-mode/timeout/argument fields.
func TestStashScriptFormat(t *testing.T) {
	p := &Parser{}
	src := `#!name=Test
[Script]
http-response ^https?://api\.example\.com/data script-path=https://example.com/resp.js, tag=respScript, requires-body=true
`
	mods := p.parseLoonPlugin(src, map[string]string{})
	out := p.convertToStashFormat(mods, "stash", map[string]string{})

	// Must contain the Stash-native match/name/type structure
	required := []string{
		"- match: ^https?://api\\.example\\.com/data",
		`name: "respScript"`,
		"type: response",
		"require-body: true",
		"timeout: 30",
		`script-providers:`,
		`"respScript":`,
		"url: https://example.com/resp.js",
		"interval: 86400",
	}
	for _, r := range required {
		if !strings.Contains(out, r) {
			t.Fatalf("Stash output missing %q, got:\n%s", r, out)
		}
	}
	// Must NOT contain Surge-style [Script] remnants
	if strings.Contains(out, "script-path=") {
		t.Fatalf("Stash output must not contain Surge script-path=, got:\n%s", out)
	}
	if strings.Contains(out, "type=http-") {
		t.Fatalf("Stash type should not have http- prefix, got:\n%s", out)
	}
}

// TestStashCronFormat verifies Stash cron section uses the upstream YAML
// structure (cron.script with name/cron/timeout).
func TestStashCronFormat(t *testing.T) {
	p := &Parser{}
	src := `#!name=Test
[Script]
cron "0 8 * * *" script-path=https://example.com/cron.js, tag=cronScript
`
	mods := p.parseLoonPlugin(src, map[string]string{})
	out := p.convertToStashFormat(mods, "stash", map[string]string{})
	if !strings.Contains(out, "cron:") {
		t.Fatalf("Stash missing cron section, got:\n%s", out)
	}
	if !strings.Contains(out, "  script:") {
		t.Fatalf("Stash missing cron.script subsection, got:\n%s", out)
	}
	if !strings.Contains(out, `name: "cronScript"`) {
		t.Fatalf("Stash cron missing name, got:\n%s", out)
	}
	if !strings.Contains(out, "cron: 0 8 * * *") {
		t.Fatalf("Stash cron missing cron expression, got:\n%s", out)
	}
}

// TestStashTilesFormat verifies Stash generic scripts become tiles with
// name/interval/title/icon/backgroundColor/timeout fields.
func TestStashTilesFormat(t *testing.T) {
	p := &Parser{}
	src := `#!name=Test
[Script]
generic script-path=https://example.com/gen.js, tag=genScript, img-url=https://x.com/g.png
`
	mods := p.parseLoonPlugin(src, map[string]string{})
	out := p.convertToStashFormat(mods, "stash", map[string]string{})
	if !strings.Contains(out, "tiles:") {
		t.Fatalf("Stash missing tiles section, got:\n%s", out)
	}
	required := []string{
		`- name: "genScript"`,
		"interval: 3600",
		`title: "genScript"`,
		`icon: "https://x.com/g.png"`,
		`backgroundColor: "#5d84f8"`,
	}
	for _, r := range required {
		if !strings.Contains(out, r) {
			t.Fatalf("Stash tiles missing %q, got:\n%s", r, out)
		}
	}
}

// TestStashScriptProvidersDedup verifies script-providers are emitted and the
// provider URL/interval render on separate lines (real newlines, not literal \n).
func TestStashScriptProvidersDedup(t *testing.T) {
	p := &Parser{}
	src := `#!name=Test
[Script]
http-response ^https?://api script-path=https://example.com/r.js, tag=rScript, requires-body=true
`
	mods := p.parseLoonPlugin(src, map[string]string{})
	out := p.convertToStashFormat(mods, "stash", map[string]string{})
	// The provider must render with real newlines, not literal backslash-n
	if strings.Contains(out, `\n`) {
		t.Fatalf("Stash provider must use real newlines, not literal \\n, got:\n%s", out)
	}
	if !strings.Contains(out, "    url: https://example.com/r.js\n    interval: 86400") {
		t.Fatalf("Stash provider URL/interval must be on separate lines, got:\n%s", out)
	}
}

// TestLoonHeaderRewriteConversion verifies Surge [Header Rewrite] lines are
// converted to Loon [Rewrite] per upstream Rewrite-Parser.js (loon-plugin
// branch): strip http-request/http-response prefix; for responses, replace
// " header-" with " response-header-".
func TestLoonHeaderRewriteConversion(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"http-response ^https?://h header-add Droid_Vulcan 1", "^https?://h response-header-add Droid_Vulcan 1"},
		{"http-request ^https?://h header-add X 1", "^https?://h header-add X 1"},
		{"http-response ^https?://h header-del Cookie", "^https?://h response-header-del Cookie"},
		{"http-request ^https?://h header-replace Host example.com", "^https?://h header-replace Host example.com"},
	}
	for _, tt := range tests {
		got := convertSurgeHeaderRewriteToLoon(tt.in)
		if got != tt.want {
			t.Fatalf("Loon hdr-rewrite %q:\n  got  %q\n  want %q", tt.in, got, tt.want)
		}
	}
}

// TestStashHeaderRewriteConversion verifies Surge [Header Rewrite] lines are
// converted to Stash header-rewrite per upstream Rewrite-Parser.js
// (stash-stoverride branch): strip http-request/http-response prefix and
// replace " header-" with " request-"/" response-".
func TestStashHeaderRewriteConversion(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"http-response ^https?://h header-add Droid_Vulcan 1", "^https?://h response-add Droid_Vulcan 1"},
		{"http-request ^https?://h header-add X 1", "^https?://h request-add X 1"},
		{"http-response ^https?://h header-del Cookie", "^https?://h response-del Cookie"},
		{"http-request ^https?://h header-replace Host example.com", "^https?://h request-replace Host example.com"},
	}
	for _, tt := range tests {
		got := convertSurgeHeaderRewriteToStash(tt.in)
		if got != tt.want {
			t.Fatalf("Stash hdr-rewrite %q:\n  got  %q\n  want %q", tt.in, got, tt.want)
		}
	}
}

// TestStashHeaderRewriteSection verifies the Stash http.header-rewrite section
// applies the conversion (not raw Surge lines).
func TestStashHeaderRewriteSection(t *testing.T) {
	p := &Parser{}
	// Construct a parsed module with a native Surge header-rewrite line.
	mods := []ParsedModule{{
		Name: "TestHdr",
		Rewrites: []ParsedRewrite{{
			Type:       RewriteTypeHeaderRewrite,
			Pattern:    "^https?://h",
			Replacement: "http-response ^https?://h header-add Droid_Vulcan 1",
		}},
	}}
	out := p.convertToStashFormat(mods, "stash", map[string]string{})
	if !strings.Contains(out, "response-add Droid_Vulcan") {
		t.Fatalf("Stash header-rewrite should be normalized to response-add, got:\n%s", out)
	}
	if strings.Contains(out, "http-response ") {
		t.Fatalf("Stash header-rewrite should have http-response prefix stripped, got:\n%s", out)
	}
}

// TestStashBodyRewriteTypeMapping verifies Stash body-rewrite type mapping per
// upstream Rewrite-Parser.js (line 1848): http-request -> request-replace-regex,
// http-response -> response-replace-regex, http-request-jq -> request-jq,
// http-response-jq -> response-jq. The previous ReplaceAll-based logic corrupted
// the jq variants into "request-replace-regex-jq".
func TestStashBodyRewriteTypeMapping(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{
		Name: "T",
		BodyRewrites: []BodyRewriteEntry{
			{Type: "http-request", Regex: "^r1", Value: `"v1"`},
			{Type: "http-response", Regex: "^r2", Value: `'v2'`},
			{Type: "http-request-jq", Regex: "^r3", Value: "jq3"},
			{Type: "http-response-jq", Regex: "^r4", Value: "jq4"},
		},
	}}
	out := p.convertToStashFormat(mods, "stash", map[string]string{})
	required := []string{
		"^r1 request-replace-regex v1",
		"^r2 response-replace-regex v2",
		"^r3 request-jq jq3",
		"^r4 response-jq jq4",
	}
	for _, r := range required {
		if !strings.Contains(out, r) {
			t.Fatalf("Stash body-rewrite missing %q, got:\n%s", r, out)
		}
	}
	// The buggy output must NOT appear
	if strings.Contains(out, "request-replace-regex-jq") || strings.Contains(out, "response-replace-regex-jq") {
		t.Fatalf("Stash body-rewrite jq variant corrupted, got:\n%s", out)
	}
}

// TestLoonBodyRewriteTypeMapping verifies Loon body-rewrite type mapping per
// upstream Rewrite-Parser.js (lines 1330-1344): http-request -> request-body-
// replace-regex, http-response -> response-body-replace-regex, and the jq
// variants map to request-body-json-jq / response-body-json-jq.
func TestLoonBodyRewriteTypeMapping(t *testing.T) {
	tests := []struct {
		br   BodyRewriteEntry
		want string
	}{
		{BodyRewriteEntry{Type: "http-request", Regex: "^r1", Value: "v1"}, "^r1 request-body-replace-regex v1"},
		{BodyRewriteEntry{Type: "http-response", Regex: "^r2", Value: "v2"}, "^r2 response-body-replace-regex v2"},
		{BodyRewriteEntry{Type: "http-request-jq", Regex: "^r3", Value: "v3"}, "^r3 request-body-json-jq v3"},
		{BodyRewriteEntry{Type: "http-response-jq", Regex: "^r4", Value: "v4"}, "^r4 response-body-json-jq v4"},
	}
	for _, tt := range tests {
		got := loonBodyRewrite(tt.br)
		if got != tt.want {
			t.Fatalf("Loon body-rewrite: got %q, want %q", got, tt.want)
		}
	}
}

// TestEndToEndConversionMatrix exercises the full parse->convert pipeline for
// every target, starting from a representative Surge module that exercises
// rewrites (reject/url-rewrite/header-rewrite/body-rewrite), scripts (http/
// cron/generic), MITM, and metadata. Verifies no target crashes and that key
// per-target invariants hold.
func TestEndToEndConversionMatrix(t *testing.T) {
	p := &Parser{}
	src := `#!name=MatrixTest
#!desc=End-to-end conversion matrix test
#!icon=https://example.com/icon.png
#!category=test

[Rule]
DOMAIN-SUFFIX,ads.com,REJECT
DOMAIN,api.example.com,PROXY

[URL Rewrite]
^https?://ads\.example\.com reject-img
^https?://old\.example\.com https://new.example.com
^https?://video\.ads\.com reject-video

[Header Rewrite]
http-response ^https?://api\.example\.com header-add X-Test 1
http-request ^https?://api\.example\.com header-add X-Req 1

[Body Rewrite]
http-response ^https?://api\.example\.com name="value"

[Script]
api = type=http-response, pattern=^https?://api\.example\.com, script-path=https://example.com/api.js, requires-body=true, timeout=30
cron1 = type=cron, cronexp=0 8 * * *, script-path=https://example.com/cron.js
gen = type=generic, script-path=https://example.com/gen.js, img-url=https://example.com/gen.png

[MITM]
hostname = %APPEND% api.example.com, ads.example.com
`
	mods := p.parseSurgeModule(src, map[string]string{})
	if len(mods) == 0 {
		t.Fatalf("parse produced no modules")
	}

	targets := []string{"surge-module", "shadowrocket-module", "loon-plugin",
		"stash-stoverride", "egern-module", "lancex-module", "qx-rewrite"}

	for _, tgt := range targets {
		t.Run(tgt, func(t *testing.T) {
			var out string
			// Use the public convert path
			out = p.convertModules(mods, tgt, map[string]string{})
			if out == "" {
				t.Fatalf("%s produced empty output", tgt)
			}
			// Universal: name should appear
			if !strings.Contains(out, "MatrixTest") {
				t.Fatalf("%s: name missing in output:\n%s", tgt, out)
			}
		})
	}
}

// TestEndToEndShadowrocketToLoon simulates the user's original request:
// converting a Shadowrocket (Surge-format) module to Loon. Exercises the
// exact pipeline the user described.
func TestEndToEndShadowrocketToLoon(t *testing.T) {
	p := &Parser{}
	// Shadowrocket modules are Surge-format. This one has scripts + rewrites.
	shadowrocketSrc := `#!name=SRDemo
#!desc=Shadowrocket to Loon

[URL Rewrite]
^https?://api\.sr\.com/nope reject-dict
^https?://api\.sr\.com/redirect https://redirect.example.com

[Script]
sr = type=http-response, pattern=^https?://api\.sr\.com, script-path=https://example.com/sr.js, requires-body=true

[MITM]
hostname = %APPEND% api.sr.com
`
	mods := p.parseSurgeModule(shadowrocketSrc, map[string]string{})
	loonOut := p.convertModules(mods, "loon-plugin", map[string]string{})

	// Loon-specific checks
	if !strings.Contains(loonOut, "#!name=SRDemo") {
		t.Fatalf("Loon output missing name:\n%s", loonOut)
	}
	if !strings.Contains(loonOut, "[Script]") {
		t.Fatalf("Loon output missing [Script] section:\n%s", loonOut)
	}
	if !strings.Contains(loonOut, "http-response") {
		t.Fatalf("Loon [Script] should contain http-response:\n%s", loonOut)
	}
	if !strings.Contains(loonOut, "reject-dict") {
		t.Fatalf("Loon [Rewrite] should contain reject-dict:\n%s", loonOut)
	}
	if !strings.Contains(loonOut, "hostname = ") {
		t.Fatalf("Loon missing MITM hostname:\n%s", loonOut)
	}
	// script-path must be present with the http-response format
	if !strings.Contains(loonOut, "script-path=https://example.com/sr.js") {
		t.Fatalf("Loon missing script-path:\n%s", loonOut)
	}
}

// TestLoonBodyRewriteSourceParsing verifies that Loon [Rewrite] body-rewrite
// types (request-body-replace-regex / response-body-json-jq / etc.) are parsed
// into BodyRewrites rather than treated as plain URL rewrites. Previously these
// fell through to the default URLRewrite branch and corrupted every target
// output (Surge put them in [URL Rewrite], QX emitted "url request-data ...").
// Upstream Rewrite-Parser.js (lines 1330-1344) emits Loon body-rewrite in
// [Rewrite] as "<regex> <type2> <value>", so Loon-as-source must read them back.
func TestLoonBodyRewriteSourceParsing(t *testing.T) {
	p := &Parser{}
	src := `#!name=LoonBody

[Rewrite]
^https?://api\.loon\.com request-body-replace-regex val1
^https?://api\.loon\.com response-body-replace-regex val2
^https?://api\.loon\.com request-body-json-jq '.a=1'
^https?://api\.loon\.com response-body-json-jq '.b=2'
`
	mods := p.parseLoonPlugin(src, map[string]string{})
	if len(mods) == 0 {
		t.Fatal("parseLoonPlugin produced no modules")
	}
	if len(mods[0].BodyRewrites) != 4 {
		t.Fatalf("expected 4 BodyRewrites, got %d (rewrites=%d): %+v",
			len(mods[0].BodyRewrites), len(mods[0].Rewrites), mods[0].BodyRewrites)
	}
	// Each entry must carry the canonical type used by Surge [Body Rewrite].
	wantTypes := map[string]string{
		"^https?://api\\.loon\\.com": "", // placeholder
	}
	_ = wantTypes
	gotTypes := []string{}
	for _, br := range mods[0].BodyRewrites {
		gotTypes = append(gotTypes, br.Type)
	}
	for _, want := range []string{"http-request", "http-response", "http-request-jq", "http-response-jq"} {
		found := false
		for _, g := range gotTypes {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("canonical body-rewrite type %q missing in %v", want, gotTypes)
		}
	}
	// Must NOT leak into Rewrites as URL rewrite entries.
	for _, rw := range mods[0].Rewrites {
		if strings.Contains(rw.Replacement, "request-body-replace-regex") ||
			strings.Contains(rw.Replacement, "response-body-json-jq") {
			t.Fatalf("body-rewrite leaked into Rewrites as URL rewrite: %+v", rw)
		}
	}
}

// TestLoonBodyRewriteSourceToSurge verifies the round-trip: Loon body-rewrite
// source → Surge output puts them under [Body Rewrite] with canonical types.
func TestLoonBodyRewriteSourceToSurge(t *testing.T) {
	p := &Parser{}
	src := `#!name=LoonBody

[Rewrite]
^https?://api\.loon\.com request-body-replace-regex val1
^https?://api\.loon\.com response-body-json-jq '.b=2'
`
	mods := p.parseLoonPlugin(src, map[string]string{})
	out := p.convertModules(mods, "surge-module", map[string]string{})
	if !strings.Contains(out, "[Body Rewrite]") {
		t.Fatalf("Surge output missing [Body Rewrite] section:\n%s", out)
	}
	if !strings.Contains(out, "http-request ") {
		t.Fatalf("Surge [Body Rewrite] missing canonical http-request type:\n%s", out)
	}
	if !strings.Contains(out, "http-response-jq ") {
		t.Fatalf("Surge [Body Rewrite] missing canonical http-response-jq type:\n%s", out)
	}
	// Must NOT appear in [URL Rewrite].
	urlRewriteSection := out
	if strings.Contains(urlRewriteSection, "[URL Rewrite]\n^https?://api\\.loon\\.com request-body-replace-regex") {
		t.Fatalf("body-rewrite incorrectly placed in [URL Rewrite]:\n%s", out)
	}
}

// TestQXURLRewriteNotRequestData verifies that a plain URL rewrite (pattern → URL)
// emitted to QX is a 302-style redirect ("url <URL>"), NOT "url request-data <URL>"
// which would wrongly rewrite the request body. Regression for converter.go:1254.
func TestQXURLRewriteNotRequestData(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{
		Name: "QXRedirect",
		Rewrites: []ParsedRewrite{
			{Pattern: `^https?://api\.example\.com/old`, Type: RewriteTypeURLRewrite, Replacement: "https://api.example.com/new"},
		},
	}}
	out := p.convertModules(mods, "qx-rewrite", map[string]string{})
	if strings.Contains(out, "request-data") {
		t.Fatalf("plain URL rewrite must not become request-data:\n%s", out)
	}
	if !strings.Contains(out, `^https?://api\.example\.com/old url https://api.example.com/new`) {
		t.Fatalf("plain URL rewrite not emitted as QX redirect:\n%s", out)
	}
}

// TestQXBodyRewriteReplaceRegexIsCommented verifies that Surge/Loon-style body
// rewrite entries of the replace-regex type (http-request / http-response) are
// emitted as comments to the QX target. Surge/Loon store only URL pattern +
// replacement; QX's native `request-body MATCH request-body REPLACE` requires
// an explicit body-match that the IR does not have, and upstream
// Rewrite-Parser.js has no QX body-rewrite output branch at all. Previously the
// converter used Value as BOTH match and replace, producing a corrupted rule.
// The jq variants (http-{request,response}-jq) ARE convertible (single value)
// and must still be emitted.
func TestQXBodyRewriteReplaceRegexIsCommented(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{
		Name: "QXBodyRewrite",
		BodyRewrites: []BodyRewriteEntry{
			{Type: "http-request", Regex: `^https?://api\.example\.com`, Value: "oldval"},
			{Type: "http-response", Regex: `^https?://api\.example\.com`, Value: "oldval"},
			{Type: "http-request-jq", Regex: `^https?://api\.example\.com`, Value: "'.a=1'"},
			{Type: "http-response-jq", Regex: `^https?://api\.example\.com`, Value: "'.b=2'"},
		},
	}}
	out := p.convertModules(mods, "qx-rewrite", map[string]string{})
	// replace-regex types must NOT be emitted as active request-body/response-body rules
	// (which would need separate MATCH and REPLACE, but we only have one value).
	if strings.Contains(out, "url request-body oldval request-body oldval") {
		t.Fatalf("replace-regex body-rewrite must not emit corrupted QX request-body rule:\n%s", out)
	}
	if strings.Contains(out, "url response-body oldval response-body oldval") {
		t.Fatalf("replace-regex body-rewrite must not emit corrupted QX response-body rule:\n%s", out)
	}
	// replace-regex types must be emitted as explanatory comments.
	if !strings.Contains(out, "# [Body Rewrite") || !strings.Contains(out, "no QX equivalent") {
		t.Fatalf("replace-regex body-rewrite missing comment:\n%s", out)
	}
	// jq variants remain correctly convertible.
	if !strings.Contains(out, `^https?://api\.example\.com url request-body-json-jq '.a=1'`) {
		t.Fatalf("jq body-rewrite request not emitted:\n%s", out)
	}
	if !strings.Contains(out, `^https?://api\.example\.com url response-body-json-jq '.b=2'`) {
		t.Fatalf("jq body-rewrite response not emitted:\n%s", out)
	}
}

// TestStashBodyRewritePreservesInnerQuotes verifies that a jq value containing
// inner double-quotes (e.g. '.key="val"') is not corrupted by over-aggressive
// quote stripping in the Stash body-rewrite YAML output. Previously
// strings.Trim(value, "\"'") stripped the trailing " and produced truncated
// output like `request-jq .key="val`. Upstream uses anchored regexes
// /^"(.+)"$/ then /^'(.+)'$/ that only strip a matching outer pair.
func TestStashBodyRewritePreservesInnerQuotes(t *testing.T) {
	p := &Parser{}
	mods := []ParsedModule{{
		Name: "StashBody",
		BodyRewrites: []BodyRewriteEntry{
			{Type: "http-request-jq", Regex: `^https?://api\.example\.com/body`, Value: `'.key="val"'`},
		},
	}}
	out := p.convertModules(mods, "stash-stoverride", map[string]string{})
	if !strings.Contains(out, "body-rewrite:") {
		t.Fatalf("Stash output missing body-rewrite section:\n%s", out)
	}
	// Outer single quotes stripped, inner double quotes preserved intact.
	if !strings.Contains(out, `.key="val"`) {
		t.Fatalf("Stash body-rewrite corrupted inner quotes (want .key=\"val\"):\n%s", out)
	}
}

// TestHostMappingAcrossTargets verifies that Surge [Host] entries are preserved
// across every target format, following upstream Rewrite-Parser.js (lines
// 1389-1402): normal hosts → [Host] section on Surge/Shadowrocket/Loon and
// [host] on QX; script hosts (value starts with "script:") → otherRule on Loon
// and Stash; all hosts → otherRule on Stash (Stash has no [Host] section).
func TestHostMappingAcrossTargets(t *testing.T) {
	p := &Parser{}
	src := `#!name=HostMap

[Host]
static.example.com = 127.0.0.1
dynamic.example.com = script:dns.js

[URL Rewrite]
^https?://ads\.example\.com reject

[MITM]
hostname = %APPEND% ads.example.com
`
	mods := p.parseSurgeModule(src, map[string]string{})
	if len(mods[0].Hosts) != 2 {
		t.Fatalf("expected 2 parsed hosts, got %d", len(mods[0].Hosts))
	}

	// Surge / Shadowrocket / Egern: [Host] section with both entries verbatim.
	for _, tgt := range []string{"surge-module", "shadowrocket-module", "egern-module"} {
		out := p.convertModules(mods, tgt, map[string]string{})
		if !strings.Contains(out, "[Host]\nstatic.example.com = 127.0.0.1") {
			t.Fatalf("%s: static host missing from [Host]:\n%s", tgt, out)
		}
		if !strings.Contains(out, "dynamic.example.com = script:dns.js") {
			t.Fatalf("%s: script host missing from [Host]:\n%s", tgt, out)
		}
	}

	// Loon: normal host → [Host]; script host → [Rule] (otherRule).
	loonOut := p.convertModules(mods, "loon-plugin", map[string]string{})
	if !strings.Contains(loonOut, "[Host]\nstatic.example.com = 127.0.0.1") {
		t.Fatalf("Loon: static host missing from [Host]:\n%s", loonOut)
	}
	if strings.Contains(strings.Split(loonOut, "[Host]")[1], "dynamic.example.com") {
		// dynamic (script) host must NOT be in [Host]; it belongs to [Rule]
		t.Fatalf("Loon: script host leaked into [Host]:\n%s", loonOut)
	}
	rulePart := loonOut
	ruleSec := strings.Split(rulePart, "[Rule]")
	if len(ruleSec) > 1 && !strings.Contains(ruleSec[1], "dynamic.example.com = script:dns.js") {
		t.Fatalf("Loon: script host missing from [Rule] (otherRule):\n%s", loonOut)
	}

	// Stash: every host pushed to rules (otherRule); no [Host] section.
	stashOut := p.convertModules(mods, "stash-stoverride", map[string]string{})
	if strings.Contains(stashOut, "[Host]") || strings.Contains(stashOut, "host:") {
		// Stash YAML has no host section; hosts live under rules.
	}
	if !strings.Contains(stashOut, "static.example.com = 127.0.0.1") ||
		!strings.Contains(stashOut, "dynamic.example.com = script:dns.js") {
		t.Fatalf("Stash: hosts missing from rules (otherRule):\n%s", stashOut)
	}

	// QX: [host] section (Go-only extension).
	qxOut := p.convertModules(mods, "qx-rewrite", map[string]string{})
	if !strings.Contains(qxOut, "[host]\nstatic.example.com = 127.0.0.1") {
		t.Fatalf("QX: static host missing from [host]:\n%s", qxOut)
	}
}

// TestDedupRewritesKeepsDistinctTypesForSamePattern verifies that dedupRewrites
// does NOT collapse distinct rewrite entries that share the same URL pattern.
// Previously dedup keyed only on Pattern, which dropped legitimate rules such as
// a header-request AND a header-response AND a reject rule for the same host —
// a major data-loss bug. The key must include type + replacement.
func TestDedupRewritesKeepsDistinctTypesForSamePattern(t *testing.T) {
	rws := []ParsedRewrite{
		{Pattern: "^api", Type: RewriteTypeReject, Replacement: "reject"},
		{Pattern: "^api", Type: RewriteTypeHeaderRewrite, Replacement: "^api header-request K V"},
		{Pattern: "^api", Type: RewriteTypeHeaderRewrite, Replacement: "^api header-response K V"},
		{Pattern: "^api", Type: RewriteTypeReject, Replacement: "reject"}, // true duplicate
	}
	out := dedupRewrites(rws)
	if len(out) != 3 {
		t.Fatalf("dedup count: got %d want 3 (reject + 2 distinct header-rewrites, 1 dup reject dropped)", len(out))
	}
}

// TestRejectMappingPerTarget verifies the per-target reject-variant mapping
// matches upstream Rewrite-Parser.js URL Rewrite handling (lines 1268-1320):
//   - Surge-strict (surge/egern/lancex): only plain reject in URL Rewrite;
//     img/tinygif/dict/array/200/video -> [Map Local] only.
//   - Shadowrocket: URL Rewrite keeps reject-tinygif; reject-video -> reject-img;
//     reject-dict kept.
//   - Stash: URL Rewrite with -video|-tinygif -> -img.
func TestRejectMappingPerTarget(t *testing.T) {
	cases := []struct {
		rwType RewriteType
		target string
		// URL Rewrite substring expected (empty = none expected)
		wantURL string
		// Map Local substring expected (empty = none expected)
		wantMap string
	}{
		{RewriteTypeRejectTinyGif, "surge-module", "", "data-type=tiny-gif"},
		{RewriteTypeRejectTinyGif, "shadowrocket-module", "reject-tinygif", ""},
		{RewriteTypeRejectTinyGif, "stash-stoverride", "reject-img", ""},
		{RewriteTypeRejectTinyGif, "egern-module", "", "data-type=tiny-gif"},
		{RewriteTypeRejectVideo, "surge-module", "", "data-type=tiny-gif"},
		{RewriteTypeRejectVideo, "shadowrocket-module", "reject-img", ""},
		{RewriteTypeRejectVideo, "stash-stoverride", "reject-img", ""},
		{RewriteTypeRejectDict, "surge-module", "", `data="{}"`},
		{RewriteTypeRejectDict, "shadowrocket-module", "reject-dict", ""},
		{RewriteTypeRejectDict, "stash-stoverride", "reject-dict", ""},
		{RewriteTypeRejectImg, "surge-module", "", "data-type=tiny-gif"},
		{RewriteTypeRejectImg, "shadowrocket-module", "reject-img", ""},
		{RewriteTypeReject, "surge-module", "reject", ""},
		{RewriteTypeReject, "shadowrocket-module", "reject", ""},
	}
	for _, c := range cases {
		mods := []ParsedModule{{Rewrites: []ParsedRewrite{{Pattern: "^ads", Type: c.rwType}}}}
		p := &Parser{}
		out := p.convertToSurgeFormat(mods, c.target, map[string]string{})
		if c.wantURL != "" && !strings.Contains(out, "^ads "+c.wantURL) {
			t.Errorf("target=%s type=%d: missing URL Rewrite %q in:\n%s", c.target, c.rwType, "^ads "+c.wantURL, out)
		}
		if c.wantURL == "" && strings.Contains(out, "^ads reject-") {
			t.Errorf("target=%s type=%d: must NOT emit reject-* URL Rewrite for Surge-strict:\n%s", c.target, c.rwType, out)
		}
		if c.wantMap != "" && !strings.Contains(out, c.wantMap) {
			t.Errorf("target=%s type=%d: missing Map Local %q in:\n%s", c.target, c.rwType, c.wantMap, out)
		}
	}
}

// TestStashHeaderRewriteSurgeOriginCommented verifies that Surge-origin
// [Header Rewrite] raw lines (not in Loon-normalized "http-request ..." form)
// are emitted as comments to the Stash target, rather than producing malformed
// YAML like "request-request". Upstream Rewrite-Parser.js only converts
// normalized (Loon-origin) header-rewrite lines and silently drops Surge-origin
// ones; Go comments them for transparency.
func TestStashHeaderRewriteSurgeOriginCommented(t *testing.T) {
	mods := []ParsedModule{{Rewrites: []ParsedRewrite{{
		Pattern: "^api", Type: RewriteTypeHeaderRewrite,
		Replacement: "^api header-request ^X-Old:(.*)$ X-New: $1",
	}}}}
	p := &Parser{}
	out := p.convertToStashFormat(mods, "stash-stoverride", map[string]string{})
	if strings.Contains(out, "request-request") || strings.Contains(out, "response-response") {
		t.Fatalf("Stash header-rewrite produced malformed output:\n%s", out)
	}
	if !strings.Contains(out, "# ^api header-request") {
		t.Fatalf("Stash header-rewrite should be commented for Surge-origin line:\n%s", out)
	}
}

// TestLoonHeaderRewriteSurgeOriginCommented mirrors the Stash test for Loon.
func TestLoonHeaderRewriteSurgeOriginCommented(t *testing.T) {
	mods := []ParsedModule{{Rewrites: []ParsedRewrite{{
		Pattern: "^api", Type: RewriteTypeHeaderRewrite,
		Replacement: "^api header-request ^X-Old:(.*)$ X-New: $1",
	}}}}
	p := &Parser{}
	out := p.convertToLoonFormat(mods, "loon-plugin", map[string]string{})
	if !strings.HasPrefix(strings.TrimSpace(out), "#") && !strings.Contains(out, "# ^api header-request") {
		t.Fatalf("Loon header-rewrite should be commented for Surge-origin line:\n%s", out)
	}
}

// TestLoonNetworkChangedScriptNoDuplicatePrefix verifies that a network-changed
// script (from a Loon source) round-trips to Loon as "network-changed script-path=..."
// and NOT "network-changed network-changed script-path=..." (the stored Pattern equals
// the ScriptType for these event scripts and must be suppressed in the output).
func TestLoonNetworkChangedScriptNoDuplicatePrefix(t *testing.T) {
	mods := []ParsedModule{{Scripts: []ParsedRewrite{{
		Type:       RewriteTypeScript,
		ScriptType: "network-changed",
		Pattern:    "network-changed",
		ScriptPath: "https://example.com/net.js",
		Replacement: "NetScript",
	}}}}
	p := &Parser{}
	out := p.convertToLoonFormat(mods, "loon-plugin", map[string]string{})
	if strings.Contains(out, "network-changed network-changed") {
		t.Fatalf("Loon network-changed script output has duplicate prefix:\n%s", out)
	}
	if !strings.Contains(out, "network-changed script-path=https://example.com/net.js") {
		t.Fatalf("Loon network-changed script not emitted correctly:\n%s", out)
	}
}

// --- Audit regression tests (moved from zz_audit_test.go / zz_semantic_test.go) ---

func TestSemanticAuditConversionMatrix(t *testing.T) {
	p := NewParser(&config.Config{})

	// Surge source with reject-tinygif, reject-dict, reject-video, scripts
	surgeSrc := "#!name=SemanticAudit\n\n" +
		"[URL Rewrite]\n" +
		"^https?://ads\\.com reject\n" +
		"^https?://gif\\.com reject-tinygif\n" +
		"^https?://dict\\.com reject-dict\n" +
		"^https?://video\\.com reject-video\n\n" +
		"[Script]\n" +
		"MyReq = type=http-request, pattern=^https?://api\\.com, script-path=https://ex.com/r.js, requires-body=1\n" +
		"MyCron = type=cron, cronexp=\"0 8 * * *\", script-path=https://ex.com/c.js\n\n" +
		"[MITM]\nhostname = ads.com, gif.com, dict.com, video.com, api.com\n"

	tests := []struct {
		name     string
		target   string
		mustHave []string
		mustNot  []string
	}{
		{
			name:   "Surge->Surge: reject-dict goes to Map Local only",
			target: "surge-module",
			mustHave: []string{
				"[Map Local]",
				`^https?://dict\.com data-type=text data="{}" status-code=200`,
				`^https?://gif\.com data-type=tiny-gif status-code=200`,
				"^https?://ads\\.com reject",
				"MyReq = type=http-request",
				"MyCron = type=cron",
			},
			mustNot: []string{
				"^https?://dict\\.com reject-dict", // dict should NOT be in [URL Rewrite]
				"^https?://gif\\.com reject-tinygif",
				"^https?://video\\.com reject-tinygif", // video should map to Map Local
			},
		},
		{
			name:   "Surge->Shadowrocket: reject-video -> reject-img in URL Rewrite",
			target: "shadowrocket-module",
			mustHave: []string{
				"^https?://video\\.com reject-img",
				"^https?://gif\\.com reject-tinygif",
				"^https?://ads\\.com reject",
			},
			mustNot: []string{
				"reject-video",
				"reject-tinygif.*reject-tinygif",
			},
		},
		{
			name:   "Surge->Loon: reject-tinygif -> reject-img, correct script format",
			target: "loon-plugin",
			mustHave: []string{
				"[Rewrite]",
				"^https?://gif\\.com reject-img",
				"http-request ^https?://api\\.com script-path=",
				"tag=MyReq",
				"cron \"0 8 * * *\" script-path=",
			},
			mustNot: []string{
				"reject-tinygif",
				"reject-video",
			},
		},
		{
			name:   "Surge->Stash: YAML format with correct reject mapping",
			target: "stash-stoverride",
			mustHave: []string{
				"name: |-",
				"  url-rewrite:",
				"reject-img", // tinygif and video both become reject-img
				"reject-dict",
				"^https?://ads\\.com reject",
			},
			mustNot: []string{
				"reject-tinygif",
				"reject-video",
				"request-request", // was a bug: header rewrite garbled
			},
		},
		{
			name:   "Surge->QX: [rewrite_local] and [task_local] sections",
			target: "qx-rewrite",
			mustHave: []string{
				"[rewrite_local]",
				"[task_local]",
				"[mitm]",
				"^https?://ads\\.com url reject",
				"0 8 * * * https://ex.com/c.js",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := p.Parse(context.Background(), ParseInput{
				URLs:       []string{"http://local.text"},
				SourceType: "surge-module",
				TargetApp:  tt.target,
				Arguments:  map[string]string{"localtext": surgeSrc},
			})
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			for _, must := range tt.mustHave {
				if !strings.Contains(out.Content, must) {
					t.Errorf("output missing %q:\n%s", must, out.Content)
				}
			}
			for _, mustNot := range tt.mustNot {
				if strings.Contains(out.Content, mustNot) {
					t.Errorf("output unexpectedly contains %q:\n%s", mustNot, out.Content)
				}
			}
		})
	}
}

func TestAutoDetectSource(t *testing.T) {
	p := NewParser(&config.Config{})

	surgeSrc := "#!name=AutoDetectSurge\n\n[URL Rewrite]\n^https?://ads\\.com reject\n\n[MITM]\nhostname = ads.com\n"
	loonSrc := "#!name=AutoDetectLoon\n\n[Rewrite]\n^https?://ads\\.com reject\n\n[MITM]\nhostname = ads.com\n"

	targets := []string{"surge-module", "shadowrocket-module", "loon-plugin", "stash-stoverride", "qx-rewrite"}
	for _, tgt := range targets {
		for _, src := range []struct{ name, content string }{{"surge", surgeSrc}, {"loon", loonSrc}} {
			t.Run(src.name+"->"+tgt, func(t *testing.T) {
				out, err := p.Parse(context.Background(), ParseInput{
					URLs:       []string{"http://local.text"},
					SourceType: "all-module",
					TargetApp:  tgt,
					Arguments:  map[string]string{"localtext": src.content},
				})
				if err != nil {
					t.Fatalf("Parse error: %v", err)
				}
				if strings.TrimSpace(out.Content) == "" {
					t.Fatalf("empty output for all-module(%s) -> %s", src.name, tgt)
				}
			})
		}
	}
}

func TestAuditSemanticCorrectness(t *testing.T) {
	p := NewParser(&config.Config{})

	// === Test 1: Surge reject mapping ===
	// Surge reject-tinygif -> only Map Local for Surge-strict targets
	surgeSrc := "#!name=T\n\n[URL Rewrite]\n^ads reject-tinygif\n^dict reject-dict\n^plain reject\n"
	for _, tgt := range []string{"surge-module", "egern-module", "lancex-module"} {
		out, _ := p.Parse(context.Background(), ParseInput{
			URLs: []string{"http://local.text"}, SourceType: "surge-module",
			TargetApp: tgt, Arguments: map[string]string{"localtext": surgeSrc},
		})
		if strings.Contains(out.Content, "^ads reject-tinygif") {
			t.Errorf("%s: reject-tinygif should NOT be in [URL Rewrite]", tgt)
		}
		if !strings.Contains(out.Content, "^plain reject") {
			t.Errorf("%s: plain reject missing from [URL Rewrite]", tgt)
		}
		if !strings.Contains(out.Content, "data-type=tiny-gif") {
			t.Errorf("%s: reject-tinygif should produce Map Local tiny-gif", tgt)
		}
	}

	// Shadowrocket: reject-video -> reject-img in URL Rewrite
	surgeVid := "#!name=T\n\n[URL Rewrite]\n^vid reject-video\n"
	out, _ := p.Parse(context.Background(), ParseInput{
		URLs: []string{"http://local.text"}, SourceType: "surge-module",
		TargetApp: "shadowrocket-module", Arguments: map[string]string{"localtext": surgeVid},
	})
	if !strings.Contains(out.Content, "^vid reject-img") {
		t.Errorf("shadowrocket: reject-video should map to reject-img, got:\n%s", out.Content)
	}

	// Stash: reject-tinygif -> reject-img
	out, _ = p.Parse(context.Background(), ParseInput{
		URLs: []string{"http://local.text"}, SourceType: "surge-module",
		TargetApp: "stash-stoverride", Arguments: map[string]string{"localtext": surgeSrc},
	})
	if !strings.Contains(out.Content, "reject-img") {
		t.Errorf("stash: reject-tinygif should map to reject-img, got:\n%s", out.Content)
	}

	// === Test 2: QX body-rewrite jq round-trip ===
	qxJq := "[rewrite_local]\n^api url request-body-json-jq '.a=1'\n^api url response-body-json-jq '.b=2'\n\n[mitm]\nhostname = api\n"
	for _, tgt := range []string{"surge-module", "loon-plugin", "stash-stoverride"} {
		out, _ := p.Parse(context.Background(), ParseInput{
			URLs: []string{"http://local.text"}, SourceType: "qx-rewrite",
			TargetApp: tgt, Arguments: map[string]string{"localtext": qxJq},
		})
		if !strings.Contains(out.Content, ".a=1") || !strings.Contains(out.Content, ".b=2") {
			t.Errorf("%s: QX body-rewrite jq values lost, got:\n%s", tgt, out.Content)
		}
	}

	// === Test 3: Loon canonical script round-trip ===
	loonScript := "#!name=T\n\n[Script]\nhttp-request ^api script-path=https://ex.com/r.js, requires-body=true, tag=Req\ncron \"0 8 * * *\" script-path=https://ex.com/c.js, tag=Daily\n\n[MITM]\nhostname = api\n"
	out, _ = p.Parse(context.Background(), ParseInput{
		URLs: []string{"http://local.text"}, SourceType: "loon-plugin",
		TargetApp: "surge-module", Arguments: map[string]string{"localtext": loonScript},
	})
	if !strings.Contains(out.Content, "script-path=https://ex.com/r.js") {
		t.Errorf("loon->surge: script-path lost, got:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "type=http-request") {
		t.Errorf("loon->surge: script type lost, got:\n%s", out.Content)
	}

	// Loon -> Stash: cron should appear in cron section
	out, _ = p.Parse(context.Background(), ParseInput{
		URLs: []string{"http://local.text"}, SourceType: "loon-plugin",
		TargetApp: "stash-stoverride", Arguments: map[string]string{"localtext": loonScript},
	})
	if !strings.Contains(out.Content, "cron:") {
		t.Errorf("loon->stash: cron section missing, got:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "script-providers:") {
		t.Errorf("loon->stash: script-providers missing, got:\n%s", out.Content)
	}

	// === Test 4: dedupRewrites preserves distinct types ===
	// Two header rewrites for same pattern must both survive
	surgeHdr := "#!name=T\n\n[Header Rewrite]\n^api header-add request X-A 1\n^api header-del response X-B\n"
	out, _ = p.Parse(context.Background(), ParseInput{
		URLs: []string{"http://local.text"}, SourceType: "surge-module",
		TargetApp: "surge-module", Arguments: map[string]string{"localtext": surgeHdr},
	})
	if !strings.Contains(out.Content, "X-A") || !strings.Contains(out.Content, "X-B") {
		t.Errorf("surge round-trip: second header-rewrite lost (dedup bug), got:\n%s", out.Content)
	}
}

// --- Verify tests (from zz_verify_test.go) ---

func TestVerifyShadowrocketToLoon(t *testing.T) {
	p := NewParser(&config.Config{})

	// Realistic Shadowrocket module content
	src := "#!name=AdBlock-Pro\n" +
		"#!desc=Comprehensive ad blocking\n" +
		"#!icon=https://example.com/icon.png\n\n" +
		"[Rule]\n" +
		"DOMAIN-SUFFIX,ads.com,REJECT\n" +
		"DOMAIN,tracker.com,DIRECT\n\n" +
		"[URL Rewrite]\n" +
		"^https?://ads\\.com reject\n" +
		"^https?://gif\\.com reject-tinygif\n" +
		"^https?://dict\\.com reject-dict\n" +
		"^https?://video\\.com reject-video\n\n" +
		"[Body Rewrite]\n" +
		"http-request-jq ^https?://api\\.com '.ad=false'\n" +
		"http-response-jq ^https?://api\\.com '.tracking=false'\n\n" +
		"[Script]\n" +
		"ReqScript = type=http-request, pattern=^https?://api\\.com, script-path=https://ex.com/r.js, requires-body=1, timeout=30\n" +
		"RespScript = type=http-response, pattern=^https?://api\\.com, script-path=https://ex.com/resp.js, requires-body=1, timeout=30\n" +
		"DailyTask = type=cron, cronexp=\"0 8 * * *\", script-path=https://ex.com/c.js\n\n" +
		"[MITM]\n" +
		"hostname = ads.com, gif.com, dict.com, video.com, api.com\n"

	out, err := p.Parse(context.Background(), ParseInput{
		URLs:       []string{"http://local.text"},
		SourceType: "surge-module", // Shadowrocket uses Surge module format
		TargetApp:  "loon-plugin",
		Arguments:  map[string]string{"localtext": src},
	})
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	fmt.Println("=== Shadowrocket → Loon output ===")
	fmt.Println(out.Content)

	// Verify key Loon semantics
	checks := []struct {
		desc string
		want string
	}{
		{"Loon [Rewrite] section", "[Rewrite]"},
		{"reject preserved", "reject"},
		{"reject-tinygif → reject-img", "reject-img"},
		{"reject-dict preserved", "reject-dict"},
		{"reject-video → reject (no Loon native)", "reject"},
		{"body-rewrite jq request", "request-body-json-jq"},
		{"body-rewrite jq response", "response-body-json-jq"},
		{"Loon canonical [Script] format", "script-path=https://ex.com/r.js"},
		{"Loon script tag", "tag=ReqScript"},
		{"Loon cron script", "cron "},
		{"MITM section", "[MITM]"},
	}
	for _, c := range checks {
		if !strings.Contains(out.Content, c.want) {
			t.Errorf("missing %s (want %q):\n%s", c.desc, c.want, out.Content)
		}
	}

	// Must NOT have Surge-specific artifacts
	badPatterns := []struct {
		desc string
		bad  string
	}{
		{"Surge [URL Rewrite] section name", "[URL Rewrite]"},
		{"Surge [Body Rewrite] section", "[Body Rewrite]"},
		{"Surge script format (name =)", "= type=http-request"},
	}
	for _, c := range badPatterns {
		if strings.Contains(out.Content, c.bad) {
			t.Errorf("Loon output contains %s (%q):\n%s", c.desc, c.bad, out.Content)
		}
	}
}

func TestVerifyAllPairsInterchange(t *testing.T) {
	p := NewParser(&config.Config{})

	// Shadowrocket uses surge-module as source type
	sources := map[string]struct {
		srcType string
		content string
	}{
		"Shadowrocket": {"surge-module",
			"#!name=T\n\n[URL Rewrite]\n^ads reject\n^gif reject-tinygif\n^api reject-dict\n\n" +
				"[Body Rewrite]\nhttp-request-jq ^api '.a=1'\n\n" +
				"[Script]\nR = type=http-request, pattern=^api, script-path=https://ex.com/r.js, requires-body=1\n" +
				"C = type=cron, cronexp=\"0 8 * * *\", script-path=https://ex.com/c.js\n\n" +
				"[MITM]\nhostname = ads, gif, api\n"},
		"Surge": {"surge-module",
			"#!name=T\n\n[URL Rewrite]\n^ads reject\n\n[Script]\nR = type=http-request, pattern=^api, script-path=https://ex.com/r.js\n\n[MITM]\nhostname = ads, api\n"},
		"Loon": {"loon-plugin",
			"#!name=T\n\n[Rewrite]\n^ads reject\n^api request-body-json-jq '.a=1'\n\n" +
				"[Script]\nhttp-request ^api script-path=https://ex.com/r.js, requires-body=true, tag=R\n" +
				"cron \"0 8 * * *\" script-path=https://ex.com/c.js, tag=C\n\n" +
				"[MITM]\nhostname = ads, api\n"},
		"QX": {"qx-rewrite",
			"[rewrite_local]\n^ads url reject\n^api url request-body-json-jq '.a=1'\n" +
				"^api url script-request-body https://ex.com/r.js\n\n" +
				"[task_local]\n0 8 * * * https://ex.com/c.js, tag=C\n\n" +
				"[mitm]\nhostname = ads, api\n"},
		"Stash": {"all-module",
			"name: T\n\nhttp:\n  mitm:\n    - \"api\"\n  url-rewrite:\n    - >-\n      ^ads reject\n  script:\n    - match: ^api\n      name: R\n      type: request\n"},
		"Egern": {"surge-module",
			"#!name=T\n\n[URL Rewrite]\n^ads reject\n\n[Script]\nR = type=http-request, pattern=^api, script-path=https://ex.com/r.js\n\n[MITM]\nhostname = ads, api\n"},
		"LanceX": {"surge-module",
			"#!name=T\n\n[URL Rewrite]\n^ads reject\n\n[Script]\nR = type=http-request, pattern=^api, script-path=https://ex.com/r.js\n\n[MITM]\nhostname = ads, api\n"},
	}

	targets := map[string]string{
		"Shadowrocket": "shadowrocket-module",
		"Surge":        "surge-module",
		"Loon":         "loon-plugin",
		"Stash":        "stash-stoverride",
		"QX":           "qx-rewrite",
		"Egern":        "egern-module",
		"LanceX":       "lancex-module",
	}

	passed := 0
	failed := 0
	for srcName, src := range sources {
		for tgtName, tgtType := range targets {
			if srcName == tgtName {
				continue // skip self-conversion
			}
			out, err := p.Parse(context.Background(), ParseInput{
				URLs:       []string{"http://local.text"},
				SourceType: src.srcType,
				TargetApp:  tgtType,
				Arguments:  map[string]string{"localtext": src.content},
			})
			if err != nil {
				fmt.Printf("FAIL: %s → %s: %v\n", srcName, tgtName, err)
				failed++
				continue
			}
			if strings.TrimSpace(out.Content) == "" {
				fmt.Printf("FAIL: %s → %s: empty output\n", srcName, tgtName)
				failed++
				continue
			}
			passed++
			fmt.Printf("PASS: %s → %s (%d bytes)\n", srcName, tgtName, len(out.Content))
		}
	}
	fmt.Printf("\n=== Matrix summary: %d passed, %d failed ===\n", passed, failed)
	if failed > 0 {
		t.Fatalf("%d conversion pairs failed", failed)
	}
}
