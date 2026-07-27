package rewrite

import (
	"strings"
	"testing"
)

// TestClassifySurgeRewriteRejectVariants covers the per-target reject-* matrix
// in classifySurgeRewrite, including the surge-strict Map Local branches and
// the shadowrocket/stash URL Rewrite branches.
func TestClassifySurgeRewriteRejectVariants(t *testing.T) {
	p := &Parser{}

	// Surge-strict (surge): reject-video -> Map Local tiny-gif
	out := surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectVideo})
	if !strings.Contains(out, "[Map Local]") || !strings.Contains(out, "data-type=tiny-gif") {
		t.Fatalf("surge reject-video should map-local tiny-gif:\n%s", out)
	}

	// Shadowrocket: reject-video -> URL Rewrite reject-img
	out = surgeStrOutput(p, "shadowrocket", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectVideo})
	if !strings.Contains(out, "reject-img") {
		t.Fatalf("shadowrocket reject-video should be URL Rewrite reject-img:\n%s", out)
	}

	// Stash: reject-video -> URL Rewrite reject-img (no Map Local)
	out = surgeStrOutput(p, "stash", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectVideo})
	if !strings.Contains(out, "reject-img") || strings.Contains(out, "[Map Local]") {
		t.Fatalf("stash reject-video should be URL Rewrite reject-img only:\n%s", out)
	}

	// Surge-strict reject-dict -> Map Local data="{}"
	out = surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectDict})
	if !strings.Contains(out, `data="{}"`) {
		t.Fatalf("surge reject-dict should map-local {}:\n%s", out)
	}
	// Shadowrocket reject-dict -> URL Rewrite reject-dict
	out = surgeStrOutput(p, "shadowrocket", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectDict})
	if !strings.Contains(out, "reject-dict") {
		t.Fatalf("shadowrocket reject-dict:\n%s", out)
	}

	// reject-array
	out = surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectArray})
	if !strings.Contains(out, `data="[]"`) {
		t.Fatalf("surge reject-array:\n%s", out)
	}

	// reject-200
	out = surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeReject200})
	if !strings.Contains(out, `data=" "`) {
		t.Fatalf("surge reject-200:\n%s", out)
	}

	// reject-img / reject-tinygif (surge-strict)
	out = surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectImg})
	if !strings.Contains(out, "data-type=tiny-gif") {
		t.Fatalf("surge reject-img:\n%s", out)
	}
	out = surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectTinyGif})
	if !strings.Contains(out, "data-type=tiny-gif") {
		t.Fatalf("surge reject-tinygif:\n%s", out)
	}

	// shadowrocket reject-tinygif -> reject-tinygif kept
	out = surgeStrOutput(p, "shadowrocket", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectTinyGif})
	if !strings.Contains(out, "reject-tinygif") {
		t.Fatalf("shadowrocket reject-tinygif kept:\n%s", out)
	}
	// stash reject-tinygif -> reject-img
	out = surgeStrOutput(p, "stash", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectTinyGif})
	if !strings.Contains(out, "reject-img") {
		t.Fatalf("stash reject-tinygif -> reject-img:\n%s", out)
	}

	// plain reject -> URL Rewrite reject (all targets)
	out = surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeReject})
	if !strings.Contains(out, "^a reject") {
		t.Fatalf("plain reject:\n%s", out)
	}
	out = surgeStrOutput(p, "surge", ParsedRewrite{Pattern: "^a", Type: RewriteTypeRejectDrop})
	if !strings.Contains(out, "^a reject") {
		t.Fatalf("reject-drop -> reject:\n%s", out)
	}
}

// surgeStrOutput runs classifySurgeRewrite indirectly through convertToSurgeFormat
// for a single rewrite, returning the formatted output string.
func surgeStrOutput(p *Parser, target string, rw ParsedRewrite) string {
	mods := []ParsedModule{{Rewrites: []ParsedRewrite{rw}}}
	return p.convertToSurgeFormat(mods, target, map[string]string{})
}

// TestClassifySurgeRewriteHeaderBody covers the fallback header/body rewrite
// branches (non-QX sources that still carry header/body types) and the mock +
// URL rewrite / header rewrite / map local pass-through branches.
func TestClassifySurgeRewriteHeaderBody(t *testing.T) {
	p := &Parser{}

	// request-header with MatchPart/ReplacePart -> Header Rewrite section
	out := surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeRequestHeader,
		MatchPart: "X-Old", ReplacePart: "X-New",
	})
	if !strings.Contains(out, "header-rewrite request-header X-Old X-New") {
		t.Fatalf("request-header fallback wrong:\n%s", out)
	}

	// response-header via Replacement "match->replace" split
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeResponseHeader,
		Replacement: "X-Old->X-New",
	})
	if !strings.Contains(out, "header-rewrite response-header X-Old X-New") {
		t.Fatalf("response-header split wrong:\n%s", out)
	}

	// request-body / response-body -> body script
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeRequestBody, Replacement: "X",
	})
	if !strings.Contains(out, "body-") || !strings.Contains(out, "type=http-request") {
		t.Fatalf("request-body script wrong:\n%s", out)
	}
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeResponseBody, Replacement: "Y",
	})
	if !strings.Contains(out, "type=http-response") {
		t.Fatalf("response-body script wrong:\n%s", out)
	}

	// request-body with explicit Arguments (uses Arguments over Replacement)
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeRequestBody, Replacement: "ignored", Arguments: "arg=val",
	})
	if !strings.Contains(out, "arg=val") {
		t.Fatalf("body script should use Arguments:\n%s", out)
	}

	// URL rewrite pass-through
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeURLRewrite, Replacement: "302 https://x.com",
	})
	if !strings.Contains(out, "^a 302 https://x.com") {
		t.Fatalf("url rewrite passthrough wrong:\n%s", out)
	}

	// Header rewrite pass-through
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeHeaderRewrite, Replacement: "in-request-header X-Old X-New",
	})
	if !strings.Contains(out, "[Header Rewrite]") || !strings.Contains(out, "in-request-header") {
		t.Fatalf("header rewrite passthrough wrong:\n%s", out)
	}

	// Map Local pass-through
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeMapLocal, Replacement: "^a data-type=text status-code=204",
	})
	if !strings.Contains(out, "[Map Local]") || !strings.Contains(out, "status-code=204") {
		t.Fatalf("map local passthrough wrong:\n%s", out)
	}
}

// TestClassifySurgeRewriteMock covers the mock Map Local branch and its
// data/data-path/status/header/base64 sub-branches.
func TestClassifySurgeRewriteMock(t *testing.T) {
	p := &Parser{}

	// Mock with data
	out := surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeMock, MockType: "text",
		MockData: "hello", MockStatus: "200",
	})
	if !strings.Contains(out, `data-type=text`) || !strings.Contains(out, `data="hello"`) || !strings.Contains(out, "status-code=200") {
		t.Fatalf("mock data branch wrong:\n%s", out)
	}

	// Mock with data-path (when MockData empty)
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeMock, MockType: "text",
		MockDataPath: "/path/data", MockHeader: "Content-Type:application/json",
	})
	if !strings.Contains(out, `data-path="/path/data"`) || !strings.Contains(out, `header="Content-Type:application/json"`) {
		t.Fatalf("mock data-path branch wrong:\n%s", out)
	}

	// Mock with base64
	out = surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeMock, MockType: "text",
		MockData: "aGVsbG8=", MockBase64: true,
	})
	if !strings.Contains(out, "data-type=base64") {
		t.Fatalf("mock base64 branch wrong:\n%s", out)
	}
}

// TestClassifySurgeRewriteEchoResponse covers the echo-response branch.
func TestClassifySurgeRewriteEchoResponse(t *testing.T) {
	p := &Parser{}
	out := surgeStrOutput(p, "surge", ParsedRewrite{
		Pattern: "^a", Type: RewriteTypeEchoResponse,
		EchoCT: "application/json", EchoURL: "https://example.com/data",
	})
	if !strings.Contains(out, "echo-") || !strings.Contains(out, "/scripts/echo-response.js") {
		t.Fatalf("echo-response script missing:\n%s", out)
	}
	if !strings.Contains(out, "argument=") {
		t.Fatalf("echo-response argument missing:\n%s", out)
	}
}
