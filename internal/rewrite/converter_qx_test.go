package rewrite

import (
	"strings"
	"testing"
)

// TestConvertQXRewriteHeaderBody covers the request/response header/body
// branches of convertQXRewrite (MatchPart/ReplacePart path and the
// Replacement "match->replace" split path), previously uncovered.
func TestConvertQXRewriteHeaderBody(t *testing.T) {
	p := &Parser{}

	got := p.convertQXRewrite(ParsedRewrite{
		Pattern:     "^api\\.x\\.com",
		Type:        RewriteTypeRequestHeader,
		MatchPart:   "X-Old",
		ReplacePart: "X-New",
	})
	if !strings.Contains(got, "url request-header X-Old request-header X-New") {
		t.Fatalf("request-header match/replace wrong: %q", got)
	}

	got = p.convertQXRewrite(ParsedRewrite{
		Pattern:     "^api\\.x\\.com",
		Type:        RewriteTypeRequestHeader,
		Replacement: "X-Old->X-New",
	})
	if !strings.Contains(got, "request-header X-Old request-header X-New") {
		t.Fatalf("request-header replacement-split wrong: %q", got)
	}

	got = p.convertQXRewrite(ParsedRewrite{
		Pattern:     "^api\\.x\\.com",
		Type:        RewriteTypeRequestHeader,
		Replacement: "X-Same",
	})
	if !strings.Contains(got, "request-header X-Same request-header X-Same") {
		t.Fatalf("request-header fallback wrong: %q", got)
	}

	got = p.convertQXRewrite(ParsedRewrite{
		Pattern:     "^api\\.x\\.com",
		Type:        RewriteTypeResponseHeader,
		MatchPart:   "X-Old",
		ReplacePart: "X-New",
	})
	if !strings.Contains(got, "url response-header X-Old response-header X-New") {
		t.Fatalf("response-header match/replace wrong: %q", got)
	}

	got = p.convertQXRewrite(ParsedRewrite{
		Pattern:     "^api\\.x\\.com",
		Type:        RewriteTypeRequestBody,
		MatchPart:   "a",
		ReplacePart: "b",
	})
	if !strings.Contains(got, "url request-body a request-body b") {
		t.Fatalf("request-body wrong: %q", got)
	}
	got = p.convertQXRewrite(ParsedRewrite{
		Pattern:     "^api\\.x\\.com",
		Type:        RewriteTypeResponseBody,
		Replacement: "X",
	})
	if !strings.Contains(got, "url response-body X response-body X") {
		t.Fatalf("response-body fallback wrong: %q", got)
	}
}

// TestConvertQXRewriteRejectAndOthers covers the reject-* switch and
// echo-response / map-local / mock branches.
func TestConvertQXRewriteRejectAndOthers(t *testing.T) {
	p := &Parser{}
	cases := []struct {
		name string
		rw   ParsedRewrite
		want string
	}{
		{"reject", ParsedRewrite{Pattern: "^a", Type: RewriteTypeReject}, "url reject"},
		{"reject-drop", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectDrop}, "url reject"},
		{"reject-video", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectVideo}, "url reject"},
		{"reject-dict", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectDict}, "url reject-dict"},
		{"reject-img", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectImg}, "url reject-img"},
		{"reject-tinygif", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectTinyGif}, "url reject-tinygif"},
		{"reject-200", ParsedRewrite{Pattern: "^a", Type: RewriteTypeReject200}, "url reject-200"},
		{"reject-array", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectArray}, "url reject-array"},
		{"echo-response", ParsedRewrite{Pattern: "^a", Type: RewriteTypeEchoResponse, EchoCT: "application/json", EchoURL: "https://x.com/d"}, "echo-response application/json https://x.com/d"},
		{"echo-response-default-ct", ParsedRewrite{Pattern: "^a", Type: RewriteTypeEchoResponse}, "echo-response text/plain"},
		{"map-local-comment", ParsedRewrite{Pattern: "^a", Type: RewriteTypeMapLocal, Replacement: "stub"}, "[Map Local"},
		{"mock-comment", ParsedRewrite{Pattern: "^a", Type: RewriteTypeMock, MockType: "json"}, "mock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := p.convertQXRewrite(c.rw)
			if !strings.Contains(got, c.want) {
				t.Fatalf("got %q, want substring %q", got, c.want)
			}
		})
	}
}

// TestConvertQXRewriteURLRewriteBranches covers the RewriteTypeURLRewrite
// 302/307, QX-keyword, bare-URL, and empty-URL branches.
func TestConvertQXRewriteURLRewriteBranches(t *testing.T) {
	p := &Parser{}
	cases := []struct {
		name string
		rw   ParsedRewrite
		want string
	}{
		{"302", ParsedRewrite{Pattern: "^a", Type: RewriteTypeURLRewrite, Replacement: "302 https://x.com/"}, "url 302 https://x.com/"},
		{"307", ParsedRewrite{Pattern: "^a", Type: RewriteTypeURLRewrite, Replacement: "307 https://y.com/"}, "url 307 https://y.com/"},
		{"qx-keyword", ParsedRewrite{Pattern: "^a", Type: RewriteTypeURLRewrite, Replacement: "reject-img"}, "url reject-img"},
		{"bare-url", ParsedRewrite{Pattern: "^a", Type: RewriteTypeURLRewrite, Replacement: "https://z.com/"}, "url https://z.com/"},
		{"empty", ParsedRewrite{Pattern: "^a", Type: RewriteTypeURLRewrite, Replacement: ""}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := p.convertQXRewrite(c.rw)
			if c.want == "" {
				if got != "" {
					t.Fatalf("got %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("got %q, want substring %q", got, c.want)
			}
		})
	}
}

// TestConvertSurgeHeaderRewriteToQX covers the matched-directive branch
// (request-header and response-header) and the malformed fallback branch.
func TestConvertSurgeHeaderRewriteToQX(t *testing.T) {
	got := convertSurgeHeaderRewriteToQX("^a\\.com header-rewrite request-header X-Old X-New")
	if !strings.Contains(got, "url request-header X-Old request-header X-New") {
		t.Fatalf("request-header directive wrong: %q", got)
	}
	got = convertSurgeHeaderRewriteToQX("^a\\.com header-rewrite response-header Cache-Control no-cache")
	if !strings.Contains(got, "url response-header Cache-Control response-header no-cache") {
		t.Fatalf("response-header directive wrong: %q", got)
	}
	got = convertSurgeHeaderRewriteToQX("malformed line")
	if !strings.HasPrefix(got, "# [Header Rewrite]") {
		t.Fatalf("fallback should be a comment, got: %q", got)
	}
}
