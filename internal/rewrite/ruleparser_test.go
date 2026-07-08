package rewrite

import (
	"strings"
	"testing"
)

func TestModifyRuleSimple(t *testing.T) {
	input := "DOMAIN,example.com,REJECT"
	result := ModifyRule(input, "surge", RuleFlags{ExtendedMatching: true})
	if !strings.Contains(result, "extended-matching") {
		t.Errorf("expected extended-matching in result, got: %s", result)
	}
}

func TestModifyRuleLogicalAND(t *testing.T) {
	input := "AND,(DOMAIN,example.com),(IP-CIDR,10.0.0.0/8),REJECT"
	result := ModifyRule(input, "surge", RuleFlags{ExtendedMatching: true})
	if !strings.Contains(result, "extended-matching") {
		t.Errorf("expected extended-matching in AND rule, got: %s", result)
	}
	if !strings.Contains(result, "REJECT") {
		t.Errorf("expected REJECT policy preserved, got: %s", result)
	}
}

func TestModifyRuleLogicalOR(t *testing.T) {
	input := "OR,(DOMAIN-SUFFIX,google.com),(DOMAIN-KEYWORD,facebook),REJECT"
	result := ModifyRule(input, "surge", RuleFlags{ExtendedMatching: true})
	if !strings.Contains(result, "extended-matching") {
		t.Errorf("expected extended-matching in OR rule, got: %s", result)
	}
}

func TestModifyRuleNOT(t *testing.T) {
	input := "NOT,(DOMAIN,ads.example.com),REJECT"
	result := ModifyRule(input, "surge", RuleFlags{ExtendedMatching: true})
	if !strings.Contains(result, "extended-matching") {
		t.Errorf("expected extended-matching in NOT rule, got: %s", result)
	}
}

func TestModifyRulePreMatching(t *testing.T) {
	input := "AND,(DOMAIN,example.com),(IP-CIDR,10.0.0.0/8),REJECT"
	result := ModifyRule(input, "surge", RuleFlags{PreMatching: true})
	if !strings.Contains(result, "pre-matching") {
		t.Errorf("expected pre-matching in result, got: %s", result)
	}
}

func TestModifyRuleNestedLogical(t *testing.T) {
	input := "AND,(OR,(DOMAIN,a.com),(DOMAIN,b.com)),(IP-CIDR,10.0.0.0/8),REJECT"
	result := ModifyRule(input, "surge", RuleFlags{ExtendedMatching: true})
	if !strings.Contains(result, "extended-matching") {
		t.Errorf("expected extended-matching in nested rule, got: %s", result)
	}
}

func TestApplySniPmLogicalRule(t *testing.T) {
	rules := []string{
		"AND,(DOMAIN,example.com),(IP-CIDR,10.0.0.0/8),REJECT",
	}
	result := ApplySniPm(rules, "example", "")
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result[0], "extended-matching") {
		t.Errorf("expected extended-matching applied to logical rule, got: %s", result[0])
	}
}
