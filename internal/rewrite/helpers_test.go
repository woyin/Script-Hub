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
