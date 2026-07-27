package rewrite

import (
	"strings"
	"testing"
)

// TestParseQXLine covers the major branches of parseQXLine that were
// previously uncovered (script-*, body-json-jq, body-replace-regex,
// echo-response, redirect, default URL rewrite).
func TestParseQXLine(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantType   RewriteType
		wantString string // substring that should appear in some field
	}{
		// Script variants
		{"script-request-header", "^a url script-request-header https://x.com/s.js", RewriteTypeScript, "http-request"},
		{"script-request-body", "^a url script-request-body https://x.com/sb.js", RewriteTypeScript, "request-body"},
		{"script-response-header", "^a url script-response-header https://x.com/sh.js", RewriteTypeScript, "http-response"},
		{"script-response-body", "^a url script-response-body https://x.com/srb.js", RewriteTypeScript, "response-body"},

		// Body JSON jq forms
		{"request-body-json-jq", "^a url request-body-json-jq '.foo'", RewriteTypeBodyRewrite, "http-request-jq"},
		{"response-body-json-jq", "^a url response-body-json-jq '.bar'", RewriteTypeBodyRewrite, "http-response-jq"},
		{"jsonjq-request-body", "^a url jsonjq-request-body '.foo'", RewriteTypeBodyRewrite, "http-request-jq"},
		{"jsonjq-response-body", "^a url jsonjq-response-body '.bar'", RewriteTypeBodyRewrite, "http-response-jq"},

		// Body replace-regex / json actions
		{"request-body-replace-regex", "^a url request-body-replace-regex old", RewriteTypeBodyRewrite, "http-request"},
		{"response-body-replace-regex", "^a url response-body-replace-regex old", RewriteTypeBodyRewrite, "http-response"},
		{"request-body-json-add", "^a url request-body-json-add .user name", RewriteTypeBodyRewrite, "http-request-jq"},
		{"response-body-json-del", "^a url response-body-json-del .session", RewriteTypeBodyRewrite, "http-response-jq"},
		{"response-body-json-replace", "^a url response-body-json-replace .id newval", RewriteTypeBodyRewrite, "http-response-jq"},

		// echo-response
		{"echo-response-full", "^a url echo-response application/json https://x.com/d", RewriteTypeEchoResponse, "application/json"},
		{"echo-response-ct-only", "^a url echo-response text/plain", RewriteTypeEchoResponse, "text/plain"},

		// reject variants
		{"reject", "^a url reject", RewriteTypeReject, ""},
		{"reject-dict", "^a url reject-dict", RewriteTypeRejectDict, ""},
		{"reject-img", "^a url reject-img", RewriteTypeRejectImg, ""},
		{"reject-tinygif", "^a url reject-tinygif", RewriteTypeRejectTinyGif, ""},
		{"reject-200", "^a url reject-200", RewriteTypeReject200, ""},
		{"reject-array", "^a url reject-array", RewriteTypeRejectArray, ""},
		{"reject-video", "^a url reject-video", RewriteTypeRejectVideo, ""},
		{"reject-drop", "^a url reject-drop", RewriteTypeRejectDrop, ""},

		// redirects
		{"302", "^a url 302 https://x.com/", RewriteTypeURLRewrite, "302 https://x.com/"},
		{"307", "^a url 307 https://y.com/", RewriteTypeURLRewrite, "307 https://y.com/"},

		// header/body with explicit match/replace
		{"request-header-match", "^a url request-header X-Old request-header X-New", RewriteTypeRequestHeader, "X-Old->X-New"},
		{"response-body-match", "^a url response-body old response-body new", RewriteTypeResponseBody, "old->new"},
		// header/body without second keyword (fallback)
		{"request-header-fallback", "^a url request-header X-Only", RewriteTypeRequestHeader, "X-Only"},

		// Default: URL rewrite
		{"default-url", "^a url https://target.com/path", RewriteTypeURLRewrite, "https://target.com/path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rw := parseQXLine(c.line)
			if rw == nil {
				t.Fatalf("parseQXLine(%q) returned nil", c.line)
			}
			if rw.Type != c.wantType {
				t.Fatalf("type = %v, want %v (line=%q)", rw.Type, c.wantType, c.line)
			}
			if c.wantString != "" {
				combined := rw.Replacement + " " + rw.MatchPart + " " + rw.ReplacePart + " " + rw.ScriptPath + " " + rw.ScriptType + " " + rw.BodyType + " " + rw.EchoCT + " " + rw.EchoURL
				if rw.BodyRewrite != nil {
					combined += " " + rw.BodyRewrite.Type + " " + rw.BodyRewrite.Value
				}
				if !strings.Contains(combined, c.wantString) {
					t.Fatalf("expected substring %q in fields, got: %+v", c.wantString, rw)
				}
			}
		})
	}
}

// TestParseQXLineEdgeCases covers nil returns and the cron-line delegation.
func TestParseQXLineEdgeCases(t *testing.T) {
	// No " url " separator
	if rw := parseQXLine("^a foo bar"); rw != nil {
		t.Fatalf("expected nil for line without 'url', got %+v", rw)
	}
	// empty pattern
	if rw := parseQXLine(" url reject"); rw != nil {
		t.Fatalf("expected nil for empty pattern, got %+v", rw)
	}
	// empty rest
	if rw := parseQXLine("^a url "); rw != nil {
		t.Fatalf("expected nil for empty rest, got %+v", rw)
	}
	// cron line delegates to parseQXCronLine
	rw := parseQXLine("5 * * * * https://x.com/s.js, tag=foo")
	if rw == nil || rw.Type != RewriteTypeScript || rw.ScriptType != "cron" {
		t.Fatalf("cron line should delegate to cron parser, got: %+v", rw)
	}
}

// TestParseQXHeaderBodyLineNoReplace covers the parseQXHeaderBodyLine branch
// where there is no second type keyword (the simpler format).
func TestParseQXHeaderBodyLineNoReplace(t *testing.T) {
	rw := &ParsedRewrite{Pattern: "^a"}
	out := parseQXHeaderBodyLine(rw, "response-header", "response-header X-Only")
	if out.Type != RewriteTypeResponseHeader || out.MatchPart != "X-Only" || out.Replacement != "X-Only" {
		t.Fatalf("no-second-keyword branch wrong: %+v", out)
	}
	if out.ReplacePart != "" {
		t.Fatalf("ReplacePart should be empty, got %q", out.ReplacePart)
	}
}
