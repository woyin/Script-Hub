package rewrite

import (
	"strings"
	"testing"
)

func TestIsQuoted(t *testing.T) {
	cases := map[string]bool{
		`"double"`:  true,
		`'single'`:  true,
		`"mismatch'`: false,
		`'mismatch"`: false,
		`plain`:      false,
		`""`:         true,
		`''`:         true,
		`"unclosed`:  false,
	}
	for in, want := range cases {
		if got := isQuoted(in); got != want {
			t.Errorf("isQuoted(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSetFlag(t *testing.T) {
	var f RuleFlags
	setFlag(&f, "extendedMatching", true)
	if !f.ExtendedMatching {
		t.Error("extendedMatching should be set")
	}
	setFlag(&f, "noResolve", true)
	if !f.NoResolve {
		t.Error("noResolve should be set")
	}
	setFlag(&f, "preMatching", true)
	if !f.PreMatching {
		t.Error("preMatching should be set")
	}
	setFlag(&f, "src", true)
	if !f.Src {
		t.Error("src should be set")
	}
	// Unknown flag → no panic, no change
	setFlag(&f, "unknown", true)
	// Setting back to false
	setFlag(&f, "extendedMatching", false)
	if f.ExtendedMatching {
		t.Error("extendedMatching should be unset")
	}
}

func TestIsMatchingParam(t *testing.T) {
	valid := []string{"no-resolve", "extended-matching", "src", "pre-matching", "NO-RESOLVE", "Extended-Matching"}
	for _, p := range valid {
		if !isMatchingParam(p) {
			t.Errorf("isMatchingParam(%q) = false, want true", p)
		}
	}
	invalid := []string{"", "unknown", "policy", "DIRECT"}
	for _, p := range invalid {
		if isMatchingParam(p) {
			t.Errorf("isMatchingParam(%q) = true, want false", p)
		}
	}
}

func TestIsRoutingPolicy(t *testing.T) {
	// 内置路由策略
	policies := []string{"DIRECT", "REJECT", "REJECT-IMG", "REJECT-VIDEO", "REJECT-DICT",
		"REJECT-ARRAY", "REJECT-DROP", "REJECT-200", "REJECT-TINYGIF", "REJECT-NO-DROP"}
	for _, p := range policies {
		if !isRoutingPolicy(p, p) {
			t.Errorf("isRoutingPolicy(%q) = false, want true", p)
		}
	}
	// reject-* 正则
	if !isRoutingPolicy("REJECT-CUSTOM", "REJECT-CUSTOM") {
		t.Error("REJECT-CUSTOM should match regex")
	}
	// {{{template}}}
	if !isRoutingPolicy("{{{myToggle}}}", "{{{myToggle}}}") {
		t.Error("{{{...}}} should be routing policy")
	}
	// 非策略
	if isRoutingPolicy("PROXY", "PROXY") {
		t.Error("PROXY is not a routing policy")
	}
	if isRoutingPolicy("my-policy", "my-policy") {
		t.Error("custom string is not a routing policy")
	}
}

func TestCheckBalancedParens(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"balanced", "DOMAIN,foo.com", false},
		{"balanced_parens", "OR,((DOMAIN,a),(DOMAIN,b))", false},
		{"unclosed", "OR,((DOMAIN,a)", true},
		{"extra_close", "OR,(DOMAIN,a))", true},
		{"nested", "AND,(NOT,(DOMAIN,a),(DOMAIN,b)),(IP-CIDR,1/8)", false},
		{"empty", "", false},
		{"no_parens", "no parens at all", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkBalancedParens(c.in)
			if c.wantErr && err == nil {
				t.Errorf("expected error for %q, got nil", c.in)
			}
			if !c.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", c.in, err)
			}
		})
	}
}

func TestCheckBalancedParens_ErrorContainsPosition(t *testing.T) {
	err := checkBalancedParens("foo)bar")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "position") {
		t.Errorf("error should contain position: %v", err)
	}
}

func TestMergeRoutingPolicy(t *testing.T) {
	flags := RuleFlags{ExtendedMatching: true}
	out := mergeRoutingPolicy(flags, "REJECT")
	if !out.ExtendedMatching {
		t.Error("flags should be preserved")
	}
}

// ─────────── parseRule / parseExpressionList integration ───────────

func TestParseRule_SimpleDomain(t *testing.T) {
	node, err := parseRule("DOMAIN,foo.com")
	if err != nil {
		t.Fatalf("parseRule err: %v", err)
	}
	if node == nil {
		t.Fatal("expected node")
	}
}

func TestParseRule_Unbalanced(t *testing.T) {
	_, err := parseRule("OR,((DOMAIN,foo.com)")
	if err == nil {
		t.Error("unbalanced parens should return error")
	}
}

func TestParseRule_ComplexLogical(t *testing.T) {
	// 复杂逻辑表达式应成功解析
	input := "OR,((DOMAIN,a.com),(NOT,(IP-CIDR,1.2.3.0/24)))"
	node, err := parseRule(input)
	if err != nil {
		t.Fatalf("complex logical parse err: %v", err)
	}
	if node == nil {
		t.Fatal("expected node")
	}
}
