package rewrite

import (
	"fmt"
	"log"
	"regexp"
	"strings"
)

// RuleFlags holds modification flags that can be applied to rule nodes.
type RuleFlags struct {
	ExtendedMatching bool
	NoResolve        bool
	PreMatching      bool
	Src              bool
}

// ruleNode represents a parsed rule tree node.
type ruleNode struct {
	Operator         string
	NodeType         string // "LOGICAL" or "VALUE"
	Value            string
	Flags            RuleFlags
	Children         []*ruleNode
	RoutingPolicy    string
	FlagsInitialized bool
}

// ModifyRule parses a Surge logical rule line and regenerates it with the
// given flags applied to all matching sub-rules, mirroring JS modifyRule.
func ModifyRule(input, platform string, flags RuleFlags) string {
	tree, err := parseRule(input)
	if err != nil {
		log.Printf("modifyRule: parse error: %v", err)
		return input
	}
	result := generateRule(tree, platform, flags)
	if result == "" {
		return input
	}
	return result
}

// --- Tokenizer ---

type tokenType int

const (
	tokenLPAREN tokenType = iota
	tokenRPAREN
	tokenCOMMA
	tokenWORD
)

type token struct {
	typ   tokenType
	value string
}

func tokenize(input string) []token {
	var tokens []token
	pos := 0
	for pos < len(input) {
		ch := input[pos]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			pos++
			continue
		}
		switch ch {
		case '(':
			tokens = append(tokens, token{tokenLPAREN, "("})
			pos++
		case ')':
			tokens = append(tokens, token{tokenRPAREN, ")"})
			pos++
		case ',':
			tokens = append(tokens, token{tokenCOMMA, ","})
			pos++
		default:
			start := pos
			for pos < len(input) {
				c := input[pos]
				if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '(' || c == ')' || c == ',' {
					break
				}
				pos++
			}
			tokens = append(tokens, token{tokenWORD, input[start:pos]})
		}
	}
	return tokens
}

// --- Parser ---

var logicalOperators = map[string]int{
	"AND": 0, // 0 means n-ary
	"OR":  0,
	"NOT": 1,
}

var valueOperators = map[string]bool{
	"DOMAIN": true, "DOMAIN-SUFFIX": true, "DOMAIN-KEYWORD": true,
	"DOMAIN-SET": true, "DOMAIN-WILDCARD": true, "IP-CIDR": true,
	"IP-CIDR6": true, "GEOIP": true, "IP-ASN": true, "RULE-SET": true,
	"URL-REGEX": true, "USER-AGENT": true, "PROCESS-NAME": true,
	"SUBNET": true, "DEST-PORT": true, "DST-PORT": true, "IN-PORT": true,
	"SRC-PORT": true, "SRC-IP": true, "PROTOCOL": true, "NETWORK": true,
	"SCRIPT": true, "CELLULAR-RADIO": true, "HOSTNAME-TYPE": true,
	"DEVICE-NAME": true, "DOMAIN-REGEX": true, "GEOSITE": true,
	"IP-SUFFIX": true, "SRC-GEOIP": true, "SRC-IP-ASN": true,
	"SRC-IP-CIDR": true, "SRC-IP-SUFFIX": true, "IN-TYPE": true,
	"IN-USER": true, "IN-NAME": true, "PROCESS-PATH": true,
	"PROCESS-PATH-REGEX": true, "PROCESS-NAME-REGEX": true,
	"UID": true, "DSCP": true, "SUB-RULE": true, "MATCH": true,
}

var matchingParams = map[string]string{
	"no-resolve":        "noResolve",
	"extended-matching": "extendedMatching",
	"src":               "src",
	"pre-matching":      "preMatching",
}

var rejectPolicyRe = regexp.MustCompile(`(?i)^REJECT(-[A-Z]+)*$`)

type parser struct {
	tokens []token
	pos    int
	input  string
}

func parseRule(input string) (*ruleNode, error) {
	if err := checkBalancedParens(input); err != nil {
		return nil, err
	}
	tokens := tokenize(input)
	p := &parser{tokens: tokens, input: input}
	tree, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	// Check remaining tokens for routing policy
	if p.pos < len(p.tokens) {
		remaining := p.tokens[p.pos:]
		if len(remaining) >= 2 && remaining[0].typ == tokenCOMMA {
			policy := strings.ToUpper(remaining[1].value)
			tree.RoutingPolicy = policy
			p.pos += 2
		}
	}
	return tree, nil
}

func (p *parser) peek(offset int) *token {
	idx := p.pos + offset
	if idx < len(p.tokens) {
		return &p.tokens[idx]
	}
	return nil
}

func (p *parser) consume() *token {
	if p.pos >= len(p.tokens) {
		return nil
	}
	t := &p.tokens[p.pos]
	p.pos++
	return t
}

func (p *parser) expect(typ tokenType) (*token, error) {
	t := p.consume()
	if t == nil || t.typ != typ {
		return nil, fmt.Errorf("expected token type %d at position %d", typ, p.pos)
	}
	return t, nil
}

func (p *parser) parseExpression() (*ruleNode, error) {
	t := p.peek(0)
	if t == nil {
		return nil, fmt.Errorf("unexpected end of input")
	}

	if t.typ == tokenLPAREN {
		p.consume() // consume '('
		exprs, err := p.parseExpressionList()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokenRPAREN); err != nil {
			return nil, err
		}
		if len(exprs) == 1 {
			return exprs[0], nil
		}
		// Wrap multiple expressions in an implicit AND
		return &ruleNode{Operator: "AND", NodeType: "LOGICAL", Children: exprs}, nil
	}

	if t.typ == tokenWORD {
		op := strings.ToUpper(p.consume().value)

		if _, isLogical := logicalOperators[op]; isLogical {
			node := &ruleNode{Operator: op, NodeType: "LOGICAL"}
			if _, err := p.expect(tokenCOMMA); err != nil {
				return nil, err
			}

			for {
				child, err := p.parseExpression()
				if err != nil {
					return nil, err
				}
				node.Children = append(node.Children, child)

				next := p.peek(0)
				if next != nil && next.typ == tokenCOMMA {
					nextNext := p.peek(1)
					if nextNext != nil && nextNext.typ == tokenWORD {
						upper := strings.ToUpper(nextNext.value)
						if isRoutingPolicy(upper, p.input) || isMatchingParam(upper) {
							break
						}
					}
					p.consume() // consume comma
				} else {
					break
				}
			}

			// Parse trailing matching params and routing policy
			for p.peek(0) != nil && p.peek(0).typ == tokenCOMMA {
				p.consume() // comma
				pt := p.consume()
				if pt == nil {
					break
				}
				pName := strings.ToLower(pt.value)
				if isRoutingPolicy(strings.ToUpper(pt.value), p.input) {
					node.RoutingPolicy = strings.ToUpper(pt.value)
				} else if flagName, ok := matchingParams[pName]; ok {
					setFlag(&node.Flags, flagName, true)
					node.FlagsInitialized = true
				}
			}
			return node, nil
		}

		if valueOperators[op] {
			node := &ruleNode{Operator: op, NodeType: "VALUE"}
			if _, err := p.expect(tokenCOMMA); err != nil {
				return nil, err
			}
			node.Value = p.collectValue()

			for p.peek(0) != nil && p.peek(0).typ == tokenCOMMA {
				nextNext := p.peek(1)
				if nextNext != nil && nextNext.typ == tokenWORD {
					upper := strings.ToUpper(nextNext.value)
					if isRoutingPolicy(upper, p.input) || isMatchingParam(upper) {
						p.consume() // comma
						pt := p.consume()
						pName := strings.ToLower(pt.value)
						if isRoutingPolicy(strings.ToUpper(pt.value), p.input) {
							node.Flags = mergeRoutingPolicy(node.Flags, strings.ToUpper(pt.value))
							_ = pName
						} else if flagName, ok := matchingParams[pName]; ok {
							setFlag(&node.Flags, flagName, true)
						}
						continue
					}
				}
				break
			}
			return node, nil
		}

		return nil, fmt.Errorf("unknown operator: %s", op)
	}

	return nil, fmt.Errorf("unexpected token: %s", t.value)
}

func (p *parser) parseExpressionList() ([]*ruleNode, error) {
	var exprs []*ruleNode
	for {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
		if p.peek(0) != nil && p.peek(0).typ == tokenCOMMA {
			p.consume()
			if p.peek(0) != nil && p.peek(0).typ == tokenRPAREN {
				break
			}
		} else {
			break
		}
	}
	return exprs, nil
}

func (p *parser) collectValue() string {
	var value strings.Builder
	depth := 0
	for p.pos < len(p.tokens) {
		t := p.peek(0)
		if t.typ == tokenLPAREN {
			depth++
			p.consume()
			value.WriteByte('(')
		} else if t.typ == tokenRPAREN {
			if depth == 0 {
				break
			}
			depth--
			p.consume()
			value.WriteByte(')')
		} else if t.typ == tokenCOMMA && depth == 0 {
			break
		} else {
			value.WriteString(t.value)
			p.consume()
		}
	}
	return value.String()
}

// --- Generator ---

var flagSupportedTypes = map[string][]string{
	"extendedMatching": {"RULE-SET", "DOMAIN-SET", "DOMAIN-KEYWORD", "DOMAIN-SUFFIX", "DOMAIN", "URL-REGEX"},
	"noResolve":        {"IP-CIDR", "IP-CIDR6", "GEOIP", "IP-ASN", "RULE-SET"},
	"src":              {"IP-CIDR", "IP-CIDR6", "GEOIP", "IP-ASN", "IP-SUFFIX"},
	"preMatching": {
		"DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-SET", "DOMAIN-WILDCARD",
		"IP-CIDR", "IP-CIDR6", "GEOIP", "IP-ASN", "SUBNET", "DEST-PORT",
		"SRC-PORT", "SRC-IP", "RULE-SET", "AND", "OR", "NOT",
	},
}

var logicalPrecedence = map[string]int{
	"NOT": 3,
	"AND": 2,
	"OR":  1,
}

func generateRule(node *ruleNode, platform string, flags RuleFlags) string {
	result := traverseTree(node, flags, "")
	return result
}

func traverseTree(node *ruleNode, flags RuleFlags, parentOp string) string {
	// Merge external flags into node
	node.Flags.ExtendedMatching = node.Flags.ExtendedMatching || flags.ExtendedMatching
	node.Flags.NoResolve = node.Flags.NoResolve || flags.NoResolve
	node.Flags.PreMatching = node.Flags.PreMatching || flags.PreMatching
	node.Flags.Src = node.Flags.Src || flags.Src

	if node.NodeType == "LOGICAL" {
		return traverseLogical(node, flags, parentOp)
	}
	if node.NodeType == "VALUE" {
		return traverseValue(node, parentOp)
	}
	return ""
}

func traverseLogical(node *ruleNode, flags RuleFlags, parentOp string) string {
	arity := logicalOperators[node.Operator]

	hasPreMatching := node.Flags.PreMatching
	if node.RoutingPolicy != "" && hasPreMatching {
		if !rejectPolicyRe.MatchString(node.RoutingPolicy) {
			hasPreMatching = false
		}
	}
	if hasPreMatching {
		allSupported := true
		for _, child := range flattenChildren(node.Children) {
			if !isTypeSupported(child.Operator, flagSupportedTypes["preMatching"]) {
				allSupported = false
				break
			}
		}
		if !allSupported {
			hasPreMatching = false
		}
	}

	var childOutputs []string
	for _, child := range node.Children {
		for _, subChild := range flattenChildren([]*ruleNode{child}) {
			output := traverseTree(subChild, flags, node.Operator)
			if output != "" {
				childOutputs = append(childOutputs, output)
			}
		}
	}

	var result string
	if arity == 1 {
		if len(childOutputs) != 1 {
			return ""
		}
		result = fmt.Sprintf("%s,(%s)", node.Operator, childOutputs[0])
	} else {
		formatted := make([]string, len(childOutputs))
		for i, output := range childOutputs {
			childOp := ""
			if idx := strings.Index(output, ","); idx >= 0 {
				childOp = output[:idx]
			}
			if needsParens(childOp, node.Operator) {
				formatted[i] = "(" + output + ")"
			} else {
				formatted[i] = output
			}
		}
		result = fmt.Sprintf("%s,(%s)", node.Operator, strings.Join(formatted, ","))
	}

	if node.RoutingPolicy != "" {
		result += "," + node.RoutingPolicy
		if hasPreMatching {
			result += ",pre-matching"
		}
	}

	if needsParens(node.Operator, parentOp) {
		result = "(" + result + ")"
	}
	return result
}

func traverseValue(node *ruleNode, parentOp string) string {
	value := node.Value
	if (node.Operator == "URL-REGEX" || node.Operator == "USER-AGENT") &&
		!isQuoted(value) {
		value = `"` + value + `"`
	}

	result := node.Operator + "," + value

	var flagStrings []string
	if node.Flags.ExtendedMatching && isTypeSupported(node.Operator, flagSupportedTypes["extendedMatching"]) {
		flagStrings = append(flagStrings, "extended-matching")
	}
	if node.Flags.NoResolve && isTypeSupported(node.Operator, flagSupportedTypes["noResolve"]) {
		flagStrings = append(flagStrings, "no-resolve")
	}
	if node.Flags.Src && isTypeSupported(node.Operator, flagSupportedTypes["src"]) {
		flagStrings = append(flagStrings, "src")
	}
	if len(flagStrings) > 0 {
		result += "," + strings.Join(flagStrings, ",")
	}

	return "(" + result + ")"
}

// --- Helpers ---

func needsParens(op, parentOp string) bool {
	if parentOp == "" {
		return false
	}
	curPrec, curOk := logicalPrecedence[op]
	parPrec, parOk := logicalPrecedence[parentOp]
	if !curOk || !parOk {
		return false
	}
	if curPrec <= parPrec {
		return true
	}
	if op == "NOT" {
		return true
	}
	return false
}

func flattenChildren(children []*ruleNode) []*ruleNode {
	var result []*ruleNode
	for _, child := range children {
		if child != nil {
			result = append(result, child)
		}
	}
	return result
}

func isTypeSupported(op string, supportedTypes []string) bool {
	for _, t := range supportedTypes {
		if t == op {
			return true
		}
	}
	return false
}

func isQuoted(s string) bool {
	return (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`))
}

func setFlag(flags *RuleFlags, name string, val bool) {
	switch name {
	case "extendedMatching":
		flags.ExtendedMatching = val
	case "noResolve":
		flags.NoResolve = val
	case "preMatching":
		flags.PreMatching = val
	case "src":
		flags.Src = val
	}
}

func isMatchingParam(name string) bool {
	_, ok := matchingParams[strings.ToLower(name)]
	return ok
}

var routingPolicyCandidates = map[string]bool{
	"DIRECT": true, "REJECT": true, "REJECT-IMG": true, "REJECT-VIDEO": true,
	"REJECT-DICT": true, "REJECT-ARRAY": true, "REJECT-DROP": true,
	"REJECT-200": true, "REJECT-TINYGIF": true, "REJECT-NO-DROP": true,
}

func isRoutingPolicy(upper string, input string) bool {
	if routingPolicyCandidates[upper] {
		return true
	}
	if rejectPolicyRe.MatchString(upper) {
		return true
	}
	// Check for {{{template}}} policies
	if strings.HasPrefix(upper, "{{{") && strings.HasSuffix(upper, "}}}") {
		return true
	}
	return false
}

func mergeRoutingPolicy(flags RuleFlags, _ string) RuleFlags {
	return flags
}

func checkBalancedParens(input string) error {
	depth := 0
	for i, ch := range input {
		if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
			if depth < 0 {
				return fmt.Errorf("unbalanced parentheses at position %d", i)
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced parentheses: %d unclosed", depth)
	}
	return nil
}
