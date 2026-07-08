package converter

import (
	"strings"
	"testing"
)

func TestWrapTryCatch(t *testing.T) {
	out := wrapTryCatch("var x = 1")
	if !strings.Contains(out, "try {") || !strings.Contains(out, "catch (e)") {
		t.Fatalf("try-catch wrapper missing:\n%s", out)
	}
	if !strings.Contains(out, "var x = 1") {
		t.Fatalf("original content lost:\n%s", out)
	}
	if !strings.Contains(out, "_scriptSonverterCompatibilityType") {
		t.Fatalf("compatibility type detection missing:\n%s", out)
	}
}

func TestBuildMockScript(t *testing.T) {
	out := buildMockScript("hello body")
	if !strings.Contains(out, "done = $done") {
		t.Fatalf("mock script missing done wrapper:\n%s", out)
	}
	if !strings.Contains(out, `"hello body"`) {
		t.Fatalf("mock script missing body JSON:\n%s", out)
	}
	if !strings.Contains(out, "status: 200") {
		t.Fatalf("mock script missing status:\n%s", out)
	}
}

func TestUtf8ContentType(t *testing.T) {
	cases := map[string]string{
		"text/plain":              "text/plain; charset=UTF-8",
		"application/json":        "application/json; charset=UTF-8",
		"text/html; charset=gbk":   "text/html; charset=gbk",
		"image/png":                "image/png",
		"application/octet-stream": "application/octet-stream; charset=UTF-8",
	}
	for in, want := range cases {
		got := utf8ContentType(in)
		if got != want {
			t.Errorf("utf8ContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyHeaderOverrides(t *testing.T) {
	hdrs := map[string]string{"Content-Type": "text/plain", "content-length": "999"}
	out := applyHeaderOverrides(hdrs, "X-Foo: bar | Content-Type: application/json", "", "surge", "https://x/y.js")
	if out["X-Foo"] != "bar" {
		t.Fatalf("custom header not set: %v", out)
	}
	if out["Content-Type"] != "application/json; charset=UTF-8" {
		t.Fatalf("content-type override+charset wrong: %q", out["Content-Type"])
	}
	if _, ok := out["content-length"]; ok {
		t.Fatalf("content-length should be stripped: %v", out)
	}
}

func TestJsDelivrGithubRaw(t *testing.T) {
	out := jsDelivrConvert("https://raw.githubusercontent.com/user/repo/dev/path/to/file.js")
	want := "https://cdn.jsdelivr.net/gh/user/repo@dev/path/to/file.js"
	if out != want {
		t.Fatalf("jsdelivr raw mismatch: got %q want %q", out, want)
	}
}