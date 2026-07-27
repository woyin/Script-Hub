package rewrite

import (
	"strings"
	"testing"
)

// loonOut runs convertLoonRewrite via convertToLoonFormat for a single rewrite.
func loonOut(p *Parser, rw ParsedRewrite) string {
	mods := []ParsedModule{{Rewrites: []ParsedRewrite{rw}}}
	return p.convertToLoonFormat(mods, "loon-plugin", map[string]string{})
}

// TestConvertLoonRewriteHeaderBody covers the request/response header/body
// branches (MatchPart/ReplacePart, Replacement "->" split, and fallback).
func TestConvertLoonRewriteHeaderBody(t *testing.T) {
	p := &Parser{}

	got := loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeRequestHeader, MatchPart: "X-Old", ReplacePart: "X-New"})
	if !strings.Contains(got, "url-request-header X-Old X-New") {
		t.Fatalf("req-header match/replace wrong:\n%s", got)
	}
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeRequestHeader, Replacement: "X-Old->X-New"})
	if !strings.Contains(got, "url-request-header X-Old X-New") {
		t.Fatalf("req-header split wrong:\n%s", got)
	}
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeRequestHeader, Replacement: "X-Fallback"})
	if !strings.Contains(got, "url-request-header X-Fallback") {
		t.Fatalf("req-header fallback wrong:\n%s", got)
	}

	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeResponseHeader, MatchPart: "X-Old", ReplacePart: "X-New"})
	if !strings.Contains(got, "url-response-header X-Old X-New") {
		t.Fatalf("resp-header match/replace wrong:\n%s", got)
	}
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeResponseHeader, Replacement: "X-Old->X-New"})
	if !strings.Contains(got, "url-response-header X-Old X-New") {
		t.Fatalf("resp-header split wrong:\n%s", got)
	}
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeResponseHeader, Replacement: "X"})
	if !strings.Contains(got, "url-response-header X") {
		t.Fatalf("resp-header fallback wrong:\n%s", got)
	}

	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeRequestBody, MatchPart: "a", ReplacePart: "b"})
	if !strings.Contains(got, "url-request-body a b") {
		t.Fatalf("req-body match/replace wrong:\n%s", got)
	}
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeRequestBody, Replacement: "X"})
	if !strings.Contains(got, "url-request-body X") {
		t.Fatalf("req-body fallback wrong:\n%s", got)
	}
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeResponseBody, MatchPart: "a", ReplacePart: "b"})
	if !strings.Contains(got, "url-response-body a b") {
		t.Fatalf("resp-body match/replace wrong:\n%s", got)
	}
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeResponseBody, Replacement: "X"})
	if !strings.Contains(got, "url-response-body X") {
		t.Fatalf("resp-body fallback wrong:\n%s", got)
	}
}

// TestConvertLoonRewriteReject covers the reject-* switch (including the
// reject-tinygif -> reject-img normalization and reject-video/drop -> reject).
func TestConvertLoonRewriteReject(t *testing.T) {
	p := &Parser{}
	cases := []struct {
		name string
		rw   ParsedRewrite
		want string
	}{
		{"reject", ParsedRewrite{Pattern: "^a", Type: RewriteTypeReject}, "^a reject"},
		{"reject-drop", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectDrop}, "^a reject"},
		{"reject-video", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectVideo}, "^a reject"},
		{"reject-dict", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectDict}, "^a reject-dict"},
		{"reject-img", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectImg}, "^a reject-img"},
		{"reject-tinygif", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectTinyGif}, "^a reject-img"},
		{"reject-200", ParsedRewrite{Pattern: "^a", Type: RewriteTypeReject200}, "^a reject-200"},
		{"reject-array", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectArray}, "^a reject-array"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := loonOut(p, c.rw)
			if !strings.Contains(got, c.want) {
				t.Fatalf("got output missing %q:\n%s", c.want, got)
			}
		})
	}
}

// TestConvertLoonRewriteURLEchoMock covers URLRewrite passthrough,
// echo-response mock-response-body, HeaderRewrite conversion, MapLocal
// passthrough, and Mock / MockRequestBody branches.
func TestConvertLoonRewriteURLEchoMock(t *testing.T) {
	p := &Parser{}

	// URL rewrite passthrough
	got := loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeURLRewrite, Replacement: "302 https://x.com"})
	if !strings.Contains(got, "^a 302 https://x.com") {
		t.Fatalf("url rewrite passthrough wrong:\n%s", got)
	}

	// Header Rewrite -> Loon [Rewrite]
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeHeaderRewrite, Replacement: "http-request ^a.com header-rewrite X-Old X-New"})
	if !strings.Contains(got, "^a.com header-rewrite X-Old X-New") {
		t.Fatalf("header-rewrite -> loon wrong:\n%s", got)
	}

	// echo-response with URL -> mock-response-body data-path
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeEchoResponse, EchoCT: "json", EchoURL: "https://x.com/d"})
	if !strings.Contains(got, "mock-response-body data-type=json data-path=https://x.com/d") {
		t.Fatalf("echo-response mock wrong:\n%s", got)
	}
	// echo-response default type
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeEchoResponse})
	if !strings.Contains(got, "mock-response-body data-type=text") {
		t.Fatalf("echo-response default type wrong:\n%s", got)
	}

	// Map Local passthrough
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeMapLocal, Replacement: "^a data-type=text status-code=204"})
	if !strings.Contains(got, "status-code=204") {
		t.Fatalf("map local passthrough wrong:\n%s", got)
	}

	// Mock response body, data precedence path > data
	got = loonOut(p, ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeMock, MockType: "json",
		MockDataPath: "/p/d", MockStatus: "200", MockBase64: true,
	})
	if !strings.Contains(got, "mock-response-body data-type=json data-path=/p/d status-code=200 mock-data-is-base64=true") {
		t.Fatalf("mock response with path+base64 wrong:\n%s", got)
	}
	// Header Rewrite response direction (http-response prefix -> response-header-)
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeHeaderRewrite, Replacement: "http-response ^a.com header-rewrite X-Old X-New"})
	if !strings.Contains(got, "response-header-rewrite") {
		t.Fatalf("header-rewrite response -> loon wrong:\n%s", got)
	}
	// Header Rewrite fallback (no http-request/response prefix -> comment)
	got = loonOut(p, ParsedRewrite{Pattern: "^a", Type: RewriteTypeHeaderRewrite, Replacement: "malformed line"})
	if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(got, "\n")), "# malformed line") && !strings.Contains(got, "# malformed line") {
		t.Fatalf("header-rewrite fallback should be comment:\n%s", got)
	}
	// Mock with data (no path)
	got = loonOut(p, ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeMock, MockType: "text", MockData: "hello",
	})
	if !strings.Contains(got, `data="hello"`) {
		t.Fatalf("mock with data wrong:\n%s", got)
	}
	// MockRequestBody
	got = loonOut(p, ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeMockRequestBody, MockType: "text",
	})
	if !strings.Contains(got, "mock-request-body") {
		t.Fatalf("mock-request-body wrong:\n%s", got)
	}
}
