package rewrite

import (
	"net/url"
	"strings"
	"testing"
)

// ─────────── ParseInputBox ───────────

func TestParseInputBox(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *InputBoxEntry
	}{
		{"simple", "#!name=MyName", &InputBoxEntry{Key: "name=", Value: "MyName"}},
		{"with_spaces", "#!name = MyName", &InputBoxEntry{Key: "name=", Value: "MyName"}},
		{"no_eq", "#!name", nil},
		{"empty", "", nil},
		{"no_bang_prefix", "key=value", &InputBoxEntry{Key: "key=", Value: "value"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseInputBox(c.in)
			if c.want == nil {
				if got != nil {
					t.Errorf("ParseInputBox(%q) = %+v, want nil", c.in, got)
				}
				return
			}
			if got == nil {
				t.Errorf("ParseInputBox(%q) = nil, want %+v", c.in, c.want)
				return
			}
			if got.Key != c.want.Key || got.Value != c.want.Value {
				t.Errorf("ParseInputBox(%q) = {Key:%q Value:%q}, want %+v", c.in, got.Key, got.Value, c.want)
			}
		})
	}
}

// ─────────── extractBlockComment ───────────

func TestExtractBlockComment(t *testing.T) {
	// 带 /* ... */ 包裹的应提取内部内容
	body := "/*\n" + `#!name=Test
DOMAIN,foo.com
` + "*/"
	out := extractBlockComment(body)
	if !strings.Contains(out, "DOMAIN,foo.com") {
		t.Errorf("should extract inner content:\n%s", out)
	}
	if strings.Contains(out, "/*") {
		t.Errorf("should strip block comment markers:\n%s", out)
	}
}

func TestExtractBlockComment_NoBlock(t *testing.T) {
	body := "#!name=Test\nDOMAIN,foo.com"
	out := extractBlockComment(body)
	if out != body {
		t.Errorf("no block comment → should return body unchanged: got %q", out)
	}
}

// ─────────── isQXRewriteKeyword ───────────

func TestIsQXRewriteKeyword(t *testing.T) {
	keywords := []string{
		"request-header", "response-header", "request-body", "response-body",
		"echo-response", "reject", "reject-dict", "reject-img", "reject-tinygif",
		"reject-200", "reject-array", "request-data", "302", "307",
	}
	for _, kw := range keywords {
		if !isQXRewriteKeyword(kw) {
			t.Errorf("isQXRewriteKeyword(%q) = false, want true", kw)
		}
	}
	invalid := []string{"", "unknown", "GET", "POST", "reject-", "301"}
	for _, kw := range invalid {
		if isQXRewriteKeyword(kw) {
			t.Errorf("isQXRewriteKeyword(%q) = true, want false", kw)
		}
	}
}

// ─────────── cleanRegexEscapes ───────────

func TestCleanRegexEscapes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`foo\.bar`, "foo.bar"},
		{`a\-b\_c`, "a-b_c"},
		{`\(\)\[\]`, "()[]"},
		{`\?\*\$`, "?*$"},
		{`\^\|\{\}`, "^|{}"},
		{`\+`, "+"},
		{`path\\to`, `path\to`},
		{`no-escapes`, "no-escapes"},
		{``, ""},
	}
	for _, c := range cases {
		if got := cleanRegexEscapes(c.in); got != c.want {
			t.Errorf("cleanRegexEscapes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─────────── sanitizeName ───────────

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"^https?://api.example.com", "_https___api_example_com"},
		{"simple", "simple"},
		{"api_v2-test", "api_v2-test"},
		{"...dots...", "dots"},
		{"", "script"},
		{strings.Repeat("a", 50), strings.Repeat("a", 40)}, // 截断到 40
	}
	for _, c := range cases {
		got := sanitizeName(c.in)
		if c.in == "" && got != "script" {
			t.Errorf("sanitizeName(empty) = %q, want script", got)
			continue
		}
		if len(c.in) >= 1 && c.in != "" && len(got) > 40 {
			t.Errorf("sanitizeName(%q) too long: %d chars", c.in, len(got))
		}
	}
}

func TestSanitizeName_Truncation(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := sanitizeName(long)
	if len(got) != 40 {
		t.Errorf("truncated length = %d, want 40", len(got))
	}
}

// ─────────── filterCommented ───────────

func TestFilterCommented(t *testing.T) {
	lines := []string{
		"#!name=Keep",
		"# comment",
		"DOMAIN,foo.com",
		"  # indented comment",
		"[Section]",
	}
	out := filterCommented(lines)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "#!name=Keep") {
		t.Errorf("#! line should be kept:\n%s", joined)
	}
	if !strings.Contains(joined, "DOMAIN,foo.com") {
		t.Errorf("active rule should be kept:\n%s", joined)
	}
	if !strings.Contains(joined, "[Section]") {
		t.Errorf("section header should be kept:\n%s", joined)
	}
	if strings.Contains(joined, "# comment") {
		t.Errorf("comment should be dropped:\n%s", joined)
	}
	if strings.Contains(joined, "indented comment") {
		t.Errorf("indented comment should be dropped:\n%s", joined)
	}
}

// ─────────── loonMockContentType ───────────

func TestLoonMockContentType(t *testing.T) {
	cases := map[string]string{
		"json":       "Content-Type:application/json",
		"text":       "Content-Type:text/plain",
		"plain":      "Content-Type:text/plain",
		"css":        "Content-Type:text/css",
		"html":       "Content-Type:text/html",
		"javascript": "Content-Type:text/javascript",
		"png":        "Content-Type:image/png",
		"gif":        "Content-Type:image/gif",
		"jpeg":       "Content-Type:image/jpeg",
		"tiff":       "Content-Type:image/tiff",
		"svg":        "Content-Type:image/svg+xml",
		"mp4":        "Content-Type:video/mp4",
		"form-data":  "Content-Type:application/x-www-form-urlencoded",
		"unknown":    "",
		"":           "",
	}
	for in, want := range cases {
		if got := loonMockContentType(in); got != want {
			t.Errorf("loonMockContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─────────── parseHostnamesFromValue ───────────

func TestParseHostnamesFromValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "a.com,b.com", []string{"a.com", "b.com"}},
		{"with_markers", "%APPEND% a.com, %INSERT% b.com", []string{"a.com", "b.com"}},
		{"trim_spaces", "  a.com  ,  b.com ", []string{"a.com", "b.com"}},
		{"empty", "", nil},
		{"only_markers", "%APPEND%", nil},
		{"trailing_comma", "a.com,", []string{"a.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseHostnamesFromValue(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// ─────────── parseCommaArgument ───────────

func TestParseCommaArgument(t *testing.T) {
	// 格式: key = type,value,tag=...
	in := "mykey = input,default-value,tag=My Label, desc=desc"
	out := parseCommaArgument(in)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].Key != "mykey" {
		t.Errorf("Key = %q, want mykey", out[0].Key)
	}
	if out[0].Type != "input" {
		t.Errorf("Type = %q, want input", out[0].Type)
	}
	if out[0].Value != "default-value" {
		t.Errorf("Value = %q", out[0].Value)
	}
}

func TestParseCommaArgument_NoMatch(t *testing.T) {
	out := parseCommaArgument("no match here")
	if out != nil {
		t.Errorf("no match → want nil, got %+v", out)
	}
}

// ─────────── qxJsonActionToBodyRewrite ───────────

func TestQxJsonActionToBodyRewrite_Del(t *testing.T) {
	out := qxJsonActionToBodyRewrite("^api\\.x", "http-response", "json-del", "key1 key2")
	if out == nil {
		t.Fatal("json-del should produce an entry")
	}
	if out.Type != "http-response-jq" {
		t.Errorf("Type = %q", out.Type)
	}
	if !strings.Contains(out.Value, "delpaths") {
		t.Errorf("json-del should use delpaths: %q", out.Value)
	}
	if !strings.Contains(out.Value, "key1") {
		t.Errorf("key1 missing: %q", out.Value)
	}
}

func TestQxJsonActionToBodyRewrite_Add(t *testing.T) {
	out := qxJsonActionToBodyRewrite("^api\\.x", "http-response", "json-add", "field1 value1")
	if out == nil {
		t.Fatal("json-add should produce an entry")
	}
	if !strings.Contains(out.Value, "setpath") {
		t.Errorf("json-add should use setpath: %q", out.Value)
	}
}

func TestQxJsonActionToBodyRewrite_Replace(t *testing.T) {
	out := qxJsonActionToBodyRewrite("^api\\.x", "http-response", "json-replace", "field value")
	if out == nil {
		t.Fatal("json-replace should produce an entry")
	}
	if !strings.Contains(out.Value, "if (getpath") {
		t.Errorf("json-replace should be conditional: %q", out.Value)
	}
}

func TestQxJsonActionToBodyRewrite_UnknownAction(t *testing.T) {
	out := qxJsonActionToBodyRewrite("^api\\.x", "http-response", "unknown-action", "key")
	if out != nil {
		t.Errorf("unknown action should return nil: %+v", out)
	}
}

func TestQxJsonActionToBodyRewrite_AddNoPairs(t *testing.T) {
	// 只有 1 个 field，无法凑成 pair → nil
	out := qxJsonActionToBodyRewrite("^api\\.x", "http-response", "json-add", "only_key")
	if out != nil {
		t.Errorf("odd fields should return nil: %+v", out)
	}
}

// ─────────── qxRewriteToScript ───────────

func TestQxRewriteToScript_RequestHeader(t *testing.T) {
	rw := &ParsedRewrite{
		Type:       RewriteTypeRequestHeader,
		Pattern:    "^api\\.x\\.com",
		MatchPart:  "old-header",
		ReplacePart: "new-header",
	}
	out := qxRewriteToScript(rw, 1)
	if out == nil {
		t.Fatal("expected script")
	}
	if out.ScriptType != "http-request" {
		t.Errorf("ScriptType = %q, want http-request", out.ScriptType)
	}
	if !strings.Contains(out.ScriptPath, "replace-header.js") {
		t.Errorf("ScriptPath = %q, want replace-header.js", out.ScriptPath)
	}
	if out.Replacement != "replaceHeader" {
		t.Errorf("Replacement = %q, want replaceHeader", out.Replacement)
	}
	// Arguments 应是 url-encoded 的 "old-header->new-header"
	decoded, _ := url.QueryUnescape(out.Arguments)
	if decoded != "old-header->new-header" {
		t.Errorf("Arguments decoded = %q", decoded)
	}
}

func TestQxRewriteToScript_ResponseBody(t *testing.T) {
	rw := &ParsedRewrite{
		Type:       RewriteTypeResponseBody,
		Pattern:    "^api\\.x",
		MatchPart:  "m",
		ReplacePart: "r",
	}
	out := qxRewriteToScript(rw, 1)
	if out.ScriptType != "http-response" {
		t.Errorf("ScriptType = %q, want http-response", out.ScriptType)
	}
	if !strings.Contains(out.ScriptPath, "replace-body.js") {
		t.Errorf("ScriptPath = %q, want replace-body.js", out.ScriptPath)
	}
	if !out.RequiresBody {
		t.Errorf("body rewrite should require body")
	}
	if out.Replacement != "replaceBody" {
		t.Errorf("Replacement = %q", out.Replacement)
	}
}

func TestQxRewriteToScript_NonRewriteType(t *testing.T) {
	// 非 header/body 类型 → 仍返回 script 但 ScriptType/Path 为空
	rw := &ParsedRewrite{Type: RewriteTypeReject, Pattern: "^x"}
	out := qxRewriteToScript(rw, 1)
	if out == nil {
		t.Fatal("expected non-nil script")
	}
	if out.ScriptType != "" {
		t.Errorf("non-header/body should have empty ScriptType: %q", out.ScriptType)
	}
}

// ─────────── GetResponse ───────────

func TestParseOutput_GetResponse(t *testing.T) {
	out := ParseOutput{
		Content: "body-content",
		Headers: map[string]string{"Content-Type": "text/plain", "X-Custom": "v"},
		Status:  201,
	}
	rd := out.GetResponse()
	if rd.Status != 201 {
		t.Errorf("Status = %d", rd.Status)
	}
	if rd.Body != "body-content" {
		t.Errorf("Body = %q", rd.Body)
	}
	if rd.Headers["X-Custom"] != "v" {
		t.Errorf("Headers = %v", rd.Headers)
	}
}

// ─────────── isMetaExtraLine ───────────

func TestIsMetaExtraLine(t *testing.T) {
	cases := map[string]bool{
		"#!name=MyName":      true,
		"#!desc=Description": true,
		"#!icon=url":         true,
		"#!arguments-desc=x": false, // 在排除列表中
		"#!arguments=x":      false,
		"#!select=x":         false,
		"#!input=x":          false,
		"#!noequals":         false, // 没有 =
		"notacomment":        false, // 不以 #! 开头
		"#comment":           false,
	}
	for in, want := range cases {
		if got := isMetaExtraLine(in); got != want {
			t.Errorf("isMetaExtraLine(%q) = %v, want %v", in, got, want)
		}
	}
}

// ─────────── parseQXHeaderBodyLine ───────────

func TestParseQXHeaderBodyLine_DoubleKeyword(t *testing.T) {
	rw := &ParsedRewrite{Pattern: "^api\\.x"}
	out := parseQXHeaderBodyLine(rw, "request-header", "request-header old-val request-header new-val")
	if out.Type != RewriteTypeRequestHeader {
		t.Errorf("Type = %v, want RewriteTypeRequestHeader", out.Type)
	}
	if out.MatchPart != "old-val" {
		t.Errorf("MatchPart = %q, want old-val", out.MatchPart)
	}
	if out.ReplacePart != "new-val" {
		t.Errorf("ReplacePart = %q, want new-val", out.ReplacePart)
	}
	if out.Replacement != "old-val->new-val" {
		t.Errorf("Replacement = %q", out.Replacement)
	}
}

func TestParseQXHeaderBodyLine_SingleKeyword(t *testing.T) {
	rw := &ParsedRewrite{Pattern: "^api"}
	out := parseQXHeaderBodyLine(rw, "response-body", "response-body just-match")
	if out.Type != RewriteTypeResponseBody {
		t.Errorf("Type = %v", out.Type)
	}
	if !out.RequiresBody {
		t.Errorf("body rewrite should set RequiresBody")
	}
	if out.BodyType != "response-body" {
		t.Errorf("BodyType = %q", out.BodyType)
	}
}

func TestParseQXHeaderBodyLine_RequestBody(t *testing.T) {
	rw := &ParsedRewrite{Pattern: "^api"}
	out := parseQXHeaderBodyLine(rw, "request-body", "request-body match")
	if out.Type != RewriteTypeRequestBody {
		t.Errorf("Type = %v", out.Type)
	}
	if out.BodyType != "request-body" {
		t.Errorf("BodyType = %q", out.BodyType)
	}
}

// ─────────── parseLoonHeaderActionLine ───────────

func TestParseLoonHeaderActionLine_Del(t *testing.T) {
	parts := []string{"^api\\.x", "http-request-header-del", "Header-Key", "Other-Key"}
	out := parseLoonHeaderActionLine(strings.Join(parts, " "), parts)
	if out == nil {
		t.Fatal("expected entry")
	}
	if !strings.Contains(out.Replacement, "http-request header-del") {
		t.Errorf("del should produce header-del: %q", out.Replacement)
	}
}

func TestParseLoonHeaderActionLine_Add(t *testing.T) {
	parts := []string{"^api", "http-response-header-add", "X-Key", "value"}
	out := parseLoonHeaderActionLine(strings.Join(parts, " "), parts)
	if out == nil {
		t.Fatal("expected entry")
	}
	if !strings.Contains(out.Replacement, "http-response header-add") {
		t.Errorf("add should produce header-add: %q", out.Replacement)
	}
}

func TestParseLoonHeaderActionLine_ReplaceRegex(t *testing.T) {
	parts := []string{"^api", "http-response-header-replace-regex", "X-Key", "pattern", "replacement"}
	out := parseLoonHeaderActionLine(strings.Join(parts, " "), parts)
	if out == nil {
		t.Fatal("expected entry")
	}
	if !strings.Contains(out.Replacement, "header-replace-regex") {
		t.Errorf("should produce header-replace-regex: %q", out.Replacement)
	}
}

func TestParseLoonHeaderActionLine_TooFewParts(t *testing.T) {
	out := parseLoonHeaderActionLine("^api http-request-header-del", []string{"^api", "http-request-header-del"})
	if out != nil {
		t.Errorf("< 3 parts should return nil: %+v", out)
	}
}

// ─────────── parseLoonScriptLine ───────────

func TestParseLoonScriptLine_CanonicalForm(t *testing.T) {
	line := `http-response ^api\.example\.com script-path=https://example.com/resp.js, tag=resp, requires-body=true`
	out := parseLoonScriptLine(line)
	if out == nil {
		t.Fatal("expected entry")
	}
	if out.ScriptType != "http-response" {
		t.Errorf("ScriptType = %q", out.ScriptType)
	}
	if out.ScriptPath != "https://example.com/resp.js" {
		t.Errorf("ScriptPath = %q", out.ScriptPath)
	}
	if !out.RequiresBody {
		t.Errorf("RequiresBody should be true")
	}
}

func TestParseLoonScriptLine_NameEqualsForm(t *testing.T) {
	// name = config 形式（不含 script-path= 时走的分支）— 虽然实际数据少见，
	// 但覆盖 parseLoonScriptLine 的 else 分支。
	line := `myScript = type=http-response, pattern=^api, requires-body=1, timeout=60, argument=arg1`
	out := parseLoonScriptLine(line)
	if out == nil {
		t.Fatal("expected entry")
	}
	if out.ScriptType != "http-response" {
		t.Errorf("ScriptType = %q", out.ScriptType)
	}
	if out.Pattern != "^api" {
		t.Errorf("Pattern = %q", out.Pattern)
	}
	if out.Timeout != 60 {
		t.Errorf("Timeout = %d, want 60", out.Timeout)
	}
	if !out.RequiresBody {
		t.Errorf("requires-body=1 → RequiresBody should be true")
	}
	if out.Arguments != "arg1" {
		t.Errorf("Arguments = %q, want arg1", out.Arguments)
	}
}

func TestParseLoonScriptLine_Empty(t *testing.T) {
	if out := parseLoonScriptLine(""); out != nil {
		t.Errorf("empty → nil expected")
	}
	if out := parseLoonScriptLine("# comment"); out != nil {
		t.Errorf("comment → nil expected")
	}
}

func TestParseLoonScriptLine_CronDirect(t *testing.T) {
	line := `cron "0 8 * * *" script-path=https://x/cron.js, tag=cronTask`
	out := parseLoonScriptLine(line)
	if out == nil {
		t.Fatal("expected entry")
	}
	if out.ScriptType != "cron" {
		t.Errorf("ScriptType = %q, want cron", out.ScriptType)
	}
	if out.CronExp != "0 8 * * *" {
		t.Errorf("CronExp = %q", out.CronExp)
	}
	if out.ScriptPath != "https://x/cron.js" {
		t.Errorf("ScriptPath = %q", out.ScriptPath)
	}
}

// ─────────── convertToGenericFormat ───────────

func TestConvertToGenericFormat(t *testing.T) {
	p := &Parser{}
	modules := []ParsedModule{{
		Name: "TestMod",
		Desc: "A description",
		Rewrites: []ParsedRewrite{
			{Pattern: "^ads", Replacement: "reject"},
		},
		Rules: []string{"DOMAIN,foo.com"},
		Scripts: []ParsedRewrite{
			{ScriptType: "http-response", ScriptPath: "https://x/s.js"},
		},
		MITM: []string{"foo.com", "bar.com"},
	}}
	out := p.convertToGenericFormat(modules, "unknown-target", map[string]string{})
	if !strings.Contains(out, "#!name=TestMod") {
		t.Errorf("name missing:\n%s", out)
	}
	if !strings.Contains(out, "#!desc=A description") {
		t.Errorf("desc missing:\n%s", out)
	}
	if !strings.Contains(out, "[Rule]") || !strings.Contains(out, "DOMAIN,foo.com") {
		t.Errorf("rule section missing:\n%s", out)
	}
	if !strings.Contains(out, "[URL Rewrite]") || !strings.Contains(out, "^ads -> reject") {
		t.Errorf("rewrite section missing:\n%s", out)
	}
	if !strings.Contains(out, "[Script]") || !strings.Contains(out, "http-response https://x/s.js") {
		t.Errorf("script section missing:\n%s", out)
	}
	if !strings.Contains(out, "[MITM]") || !strings.Contains(out, "foo.com") {
		t.Errorf("MITM section missing:\n%s", out)
	}
}

func TestConvertToGenericFormat_Empty(t *testing.T) {
	p := &Parser{}
	out := p.convertToGenericFormat([]ParsedModule{}, "", nil)
	// 空模块 → 无 name/desc
	if strings.Contains(out, "#!name=") {
		t.Errorf("empty module should not emit name:\n%s", out)
	}
}

// ─────────── keLeeIcons (HTTP integration) ───────────

func TestKeLeeIcons_FetchAndCache(t *testing.T) {
	// 由于 keLeeIconURL 是包级常量无法替换，我们测试 client==nil 分支与 cache 命中分支
	// 1. client==nil → nil
	if got := keLeeIcons(nil, nil); got != nil {
		t.Errorf("nil client → want nil, got %+v", got)
	}
	// 2. cache 已注入，client 非 nil → 返回 cache
	keLeeIconMu.Lock()
	old := keLeeIconCache
	keLeeIconCache = []keLeeIcon{{Name: "x", URL: "https://y"}}
	keLeeIconMu.Unlock()
	defer func() {
		keLeeIconMu.Lock()
		keLeeIconCache = old
		keLeeIconMu.Unlock()
	}()
	got := keLeeIcons(nil, newTestHTTPClient(t))
	if len(got) != 1 || got[0].Name != "x" {
		t.Errorf("cached lookup failed: %+v", got)
	}
}

// ─────────── uniqueStrings ───────────

func TestUniqueStrings(t *testing.T) {
	in := []string{"a", "b", "a", "c", "b", "d"}
	out := uniqueStrings(in)
	want := []string{"a", "b", "c", "d"}
	if len(out) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(out), len(want), out)
	}
	for i, v := range want {
		if out[i] != v {
			t.Errorf("[%d] = %q, want %q", i, out[i], v)
		}
	}
}

func TestUniqueStrings_Empty(t *testing.T) {
	out := uniqueStrings(nil)
	if len(out) != 0 {
		t.Errorf("nil → want empty, got %v", out)
	}
}
