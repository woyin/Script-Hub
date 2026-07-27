package rewrite

import (
	"strings"
	"testing"
)

// TestParseLoonRewriteLine covers all branches of parseLoonRewriteLine.
func TestParseLoonRewriteLine(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantType RewriteType
		wantSub  string // substring expected in some field; empty = don't check
	}{
		{"req-header-match-replace", "^a url-request-header X-Old X-New", RewriteTypeRequestHeader, "X-Old->X-New"},
		{"req-header-match-only", "^a url-request-header X-Only", RewriteTypeRequestHeader, "X-Only"},
		{"resp-header-match-replace", "^a url-response-header X-Old X-New", RewriteTypeResponseHeader, "X-Old->X-New"},
		{"resp-header-match-only", "^a url-response-header X-Only", RewriteTypeResponseHeader, "X-Only"},
		{"req-body-match-replace", "^a url-request-body old new", RewriteTypeRequestBody, "old->new"},
		{"req-body-match-only", "^a url-request-body old", RewriteTypeRequestBody, "old"},
		{"resp-body-match-replace", "^a url-response-body old new", RewriteTypeResponseBody, "old->new"},
		{"resp-body-match-only", "^a url-response-body old", RewriteTypeResponseBody, "old"},
		{"url-reject", "^a url-reject", RewriteTypeReject, ""},
		{"url-reject-dict", "^a url-reject-dict", RewriteTypeReject, ""},
		{"header-del", "^a url-request-header-del Cookie", RewriteTypeHeaderRewrite, "header-del"},
		{"header-add", "^a url-response-header-add X-Foo bar", RewriteTypeHeaderRewrite, "header-add"},
		{"header-replace", "^a url-request-header-replace X-Foo bar", RewriteTypeHeaderRewrite, "header-replace"},
		{"header-replace-regex", "^a url-response-header-replace-regex Set-Cookie pattern repl", RewriteTypeHeaderRewrite, "header-replace-regex"},
		{"body-replace-regex", "^a request-body-replace-regex old", RewriteTypeBodyRewrite, "http-request"},
		{"body-json-jq-req", "^a request-body-json-jq '.foo'", RewriteTypeBodyRewrite, "http-request-jq"},
		{"body-json-jq-resp", "^a response-body-json-jq '.bar'", RewriteTypeBodyRewrite, "http-response-jq"},
		{"url-rewrite-default", "^a https://target.com/path", RewriteTypeURLRewrite, "https://target.com/path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rw := parseLoonRewriteLine(c.line)
			if rw == nil {
				t.Fatalf("parseLoonRewriteLine(%q) returned nil", c.line)
			}
			if rw.Type != c.wantType {
				t.Fatalf("type = %v, want %v (line=%q)", rw.Type, c.wantType, c.line)
			}
			if c.wantSub != "" {
				combined := rw.Replacement + " " + rw.MatchPart + " " + rw.ReplacePart
				if rw.BodyRewrite != nil {
					combined += " " + rw.BodyRewrite.Type + " " + rw.BodyRewrite.Value
				}
				if !strings.Contains(combined, c.wantSub) {
					t.Fatalf("expected substring %q in fields, got: %+v", c.wantSub, rw)
				}
			}
		})
	}
}

// TestParseLoonRewriteLineEdge covers empty/comment lines and short lines.
func TestParseLoonRewriteLineEdge(t *testing.T) {
	if rw := parseLoonRewriteLine(""); rw != nil {
		t.Fatalf("empty line should return nil, got %+v", rw)
	}
	if rw := parseLoonRewriteLine("# comment"); rw != nil {
		t.Fatalf("comment line should return nil, got %+v", rw)
	}
	if rw := parseLoonRewriteLine("singleword"); rw != nil {
		t.Fatalf("single-word line should return nil, got %+v", rw)
	}
}

// TestParseLoonHeaderActionLine covers the request/response + del/add/replace/
// replace-regex branches of parseLoonHeaderActionLine, including the
// insufficient-args nil return.
func TestParseLoonHeaderActionLine(t *testing.T) {
	// header-del with multiple keys
	rw := parseLoonHeaderActionLine("^a url-request-header-del Cookie X-Foo", []string{"^a", "url-request-header-del", "Cookie", "X-Foo"})
	if rw == nil || !strings.Contains(rw.Replacement, "header-del") || !strings.Contains(rw.Replacement, "Cookie") {
		t.Fatalf("header-del wrong: %+v", rw)
	}
	// header-add with k/v pairs
	rw = parseLoonHeaderActionLine("^a url-response-header-add X-Foo bar X-Baz qux", []string{"^a", "url-response-header-add", "X-Foo", "bar", "X-Baz", "qux"})
	if rw == nil || !strings.Contains(rw.Replacement, "http-response") || !strings.Contains(rw.Replacement, "X-Foo") {
		t.Fatalf("header-add wrong: %+v", rw)
	}
	// header-replace-regex with k/pattern/repl triples
	rw = parseLoonHeaderActionLine("^a url-request-header-replace-regex Set-Cookie pattern repl", []string{"^a", "url-request-header-replace-regex", "Set-Cookie", "pattern", "repl"})
	if rw == nil || !strings.Contains(rw.Replacement, "Set-Cookie") || !strings.Contains(rw.Replacement, "http-request") {
		t.Fatalf("header-replace-regex wrong: %+v", rw)
	}
	// Insufficient parts -> nil
	if rw := parseLoonHeaderActionLine("^a url-request-header-del", []string{"^a", "url-request-header-del"}); rw != nil {
		t.Fatalf("insufficient parts should return nil, got %+v", rw)
	}
	// No matching entries (e.g. header-add with single token, no k/v pair)
	rw = parseLoonHeaderActionLine("^a url-request-header-add Lonely", []string{"^a", "url-request-header-add", "Lonely"})
	if rw == nil {
		t.Fatalf("should still return *ParsedRewrite even with no entries")
	}
	// Type stays as the originally-set action type when no entries built
	if rw.Type != RewriteTypeHeaderAdd {
		t.Fatalf("no-entries type should remain HeaderAdd, got %v", rw.Type)
	}
}
