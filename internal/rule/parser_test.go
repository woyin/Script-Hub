package rule

import (
	"context"
	"strings"
	"testing"

	"github.com/script-hub-org/script-hub/internal/config"
)

// parseLocal runs the parser pipeline against a local text blob and returns the
// formatted output for the given target app. Mirrors the Parse() entrypoint but
// skips remote fetching.
func parseLocal(t *testing.T, body, target string, args map[string]string) string {
	t.Helper()
	p := &Parser{}
	input := ParseInput{TargetApp: target, Arguments: args}
	rules := p.parseRules(body, input)
	out := p.formatOutput(rules, target)
	out = strings.ReplaceAll(out, "t&zd;", ",")
	out = strings.ReplaceAll(out, " ;#", " #")
	return out
}

// P0 fix #17: dot shorthand prefix must consume the leading dot.
func TestDotPrefixConsumesDot(t *testing.T) {
	out := parseLocal(t, ".example.com\n*abc.net\n+xyz.org", "surge", nil)
	for _, want := range []string{"DOMAIN-SUFFIX,example.com", "DOMAIN-SUFFIX,abc.net", "DOMAIN-SUFFIX,xyz.org"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in output, got:\n%s", want, out)
		}
	}
	// must NOT contain the buggy form with a leftover dot
	if strings.Contains(out, "DOMAIN-SUFFIX,.") {
		t.Fatalf("leftover dot detected:\n%s", out)
	}
}

// P0 fix #16: nore=true appends no-resolve only to ip-cidr/cidr6, nothing else.
func TestNoResolveOnlyWhenRequested(t *testing.T) {
	// Without nore: no no-resolve anywhere.
	out := parseLocal(t, "IP-CIDR,1.2.3.0/24\nDOMAIN,foo.com", "surge", nil)
	if strings.Contains(out, "no-resolve") {
		t.Fatalf("unexpected no-resolve without nore:\n%s", out)
	}
	// With nore: only ip rules get no-resolve, domains do not.
	out = parseLocal(t, "IP-CIDR,1.2.3.0/24\nIP-CIDR6,::1/128\nDOMAIN,foo.com", "surge",
		map[string]string{"nore": "true"})
	if !strings.Contains(out, "IP-CIDR,1.2.3.0/24,no-resolve") {
		t.Fatalf("ip-cidr missing no-resolve:\n%s", out)
	}
	if !strings.Contains(out, "IP-CIDR6,::1/128,no-resolve") {
		t.Fatalf("ip-cidr6 missing no-resolve:\n%s", out)
	}
	if strings.Contains(out, "DOMAIN,foo.com,no-resolve") {
		t.Fatalf("domain wrongly got no-resolve:\n%s", out)
	}
}

// P0 fix #15: y (include) must uncomment lines that start with #.
func TestYUncomments(t *testing.T) {
	body := "#DOMAIN,keep.me\n#DOMAIN,skip.me\nDOMAIN,normal.com"
	out := parseLocal(t, body, "surge", map[string]string{"y": "keep.me"})
	if !strings.Contains(out, "DOMAIN,keep.me") {
		t.Fatalf("y failed to uncomment keep.me:\n%s", out)
	}
	// skip.me stays commented (dropped), normal survives
	if strings.Contains(out, "DOMAIN,skip.me") {
		t.Fatalf("skip.me should remain excluded:\n%s", out)
	}
	if !strings.Contains(out, "DOMAIN,normal.com") {
		t.Fatalf("normal rule dropped:\n%s", out)
	}
}

// P0 fix: x (exclude) must mark matched rules into the excluded section.
func TestXExcludes(t *testing.T) {
	body := "DOMAIN,evil.com\nDOMAIN,good.com"
	out := parseLocal(t, body, "surge", map[string]string{"x": "evil"})
	if strings.Contains(out, "DOMAIN,evil.com") && !strings.Contains(out, "#已排除") {
		t.Fatalf("evil.com should be in excluded section:\n%s", out)
	}
	if !strings.Contains(out, "DOMAIN,good.com") {
		t.Fatalf("good.com dropped:\n%s", out)
	}
}

// P0 fix #18: commas inside regex quantifiers {N,M} must survive parsing.
func TestCommaGuard(t *testing.T) {
	out := parseLocal(t, "URL-REGEX,^foo{1,2}$", "surge", nil)
	if !strings.Contains(out, "URL-REGEX,^foo{1,2}$") {
		t.Fatalf("quantifier comma lost:\n%s", out)
	}
}

// Logical rules OR/AND/NOT: Surge emits verbatim, Stash/Loon mark unsupported.
func TestLogicalRules(t *testing.T) {
	body := "OR,((DOMAIN,a.com),(DOMAIN,b.com))\nAND,((DOMAIN,c.com))"
	out := parseLocal(t, body, "surge", nil)
	if !strings.Contains(out, "OR,((DOMAIN,a.com),(DOMAIN,b.com))") {
		t.Fatalf("surge should emit OR verbatim:\n%s", out)
	}
	outStash := parseLocal(t, body, "stash", nil)
	if !strings.Contains(outStash, "#不支持的规则") {
		t.Fatalf("stash should mark OR unsupported:\n%s", outStash)
	}
}

// Smoke test for the full Parse entrypoint with local text.
func TestParseLocalText(t *testing.T) {
	p := NewParser(&config.Config{HTTPTimeout: 5})
	out, err := p.Parse(context.Background(), ParseInput{
		URLs:      []string{"http://local.text"},
		TargetApp: "surge",
		Arguments: map[string]string{"localtext": "DOMAIN,test.com\nIP-CIDR,1.2.3.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "DOMAIN,test.com") {
		t.Fatalf("localtext not parsed:\n%s", out.Content)
	}
}
