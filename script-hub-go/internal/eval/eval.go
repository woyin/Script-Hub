package eval

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/script-hub-org/script-hub/internal/httpclient"
)

// jsReplacePattern matches JS body.replace() calls:
//   body = body.replace(/PATTERN/FLAGS, 'REPLACEMENT')
//   body = body.replace(/PATTERN/FLAGS, "REPLACEMENT")
//   body = body.replace(/PATTERN/FLAGS, REPLACEMENT)
// Also matches chained calls and body = ... patterns.
var jsReplacePattern = regexp.MustCompile(`body\s*=\s*body\.replace\(\s*/((?:[^/\\]|\\.)*)/\s*([gimsuy]*),\s*['"]?((?:[^'")\\]|\\.)*)['"]?\s*\)`)

// jsSplitJoinPattern matches JS body.split().join() calls (another replace-all idiom):
//   body = body.split('OLD').join('NEW')
var jsSplitJoinPattern = regexp.MustCompile(`body\s*=\s*body\.split\(\s*['"]([^'"]*)['"]\s*\)\.join\(\s*['"]([^'"]*)['"]\s*\)`)

// Operation represents a single text transformation.
type Operation interface {
	Apply(body string) string
}

// regexReplace implements body.replace(/pattern/flags, replacement) in Go.
type regexReplace struct {
	pattern     *regexp.Regexp
	replacement string
}

func (op *regexReplace) Apply(body string) string {
	return op.pattern.ReplaceAllString(body, op.replacement)
}

// stringReplace implements body.split(old).join(new) — simple string replacement.
type stringReplace struct {
	old string
	new string
}

func (op *stringReplace) Apply(body string) string {
	return strings.ReplaceAll(body, op.old, op.new)
}

// ParseEvalCode parses JS eval code and extracts text transformation operations.
// Supported patterns:
//   - body = body.replace(/PATTERN/FLAGS, 'REPLACEMENT')
//   - body = body.split('OLD').join('NEW')
// Unsupported patterns are logged and skipped.
func ParseEvalCode(code string) []Operation {
	if code == "" {
		return nil
	}

	var ops []Operation

	// Parse .replace() patterns
	replaceMatches := jsReplacePattern.FindAllStringSubmatch(code, -1)
	for _, m := range replaceMatches {
		patternStr := m[1]   // regex pattern (unescaped from /.../)
		flags := m[2]        // regex flags
		replacement := m[3]  // replacement string

		// Unescape the replacement string
		replacement = unescapeJSString(replacement)

		// Convert JS regex flags to Go regex flags
		goPattern := convertJSRegexToGo(patternStr, flags)
		re, err := regexp.Compile(goPattern)
		if err != nil {
			log.Printf("eval: invalid regex pattern /%s/%s: %v", patternStr, flags, err)
			continue
		}
		ops = append(ops, &regexReplace{pattern: re, replacement: replacement})
	}

	// Parse .split().join() patterns
	splitJoinMatches := jsSplitJoinPattern.FindAllStringSubmatch(code, -1)
	for _, m := range splitJoinMatches {
		old := unescapeJSString(m[1])
		new := unescapeJSString(m[2])
		ops = append(ops, &stringReplace{old: old, new: new})
	}

	// Check for unsupported patterns
	stripped := jsReplacePattern.ReplaceAllString(code, "")
	stripped = jsSplitJoinPattern.ReplaceAllString(stripped, "")
	stripped = strings.TrimSpace(stripped)
	// Remove variable declarations and simple assignments
	stripped = regexp.MustCompile(`^\s*var\s+.*$`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`^\s*let\s+.*$`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`^\s*const\s+.*$`).ReplaceAllString(stripped, "")
	stripped = regexp.MustCompile(`^\s*//.*$`).ReplaceAllString(stripped, "")
	stripped = strings.TrimSpace(stripped)
	if stripped != "" {
		log.Printf("eval: unsupported JS code ignored: %s", truncated(stripped, 200))
	}

	return ops
}

// ApplyEvalCode applies parsed JS eval operations to a body string.
func ApplyEvalCode(body string, ops []Operation) string {
	for _, op := range ops {
		body = op.Apply(body)
	}
	return body
}

// EvalParams contains all eval-related parameters.
type EvalParams struct {
	EvalScriptOri  string // evalScriptori - process original content (code)
	EvalScriptModi string // evalScriptmodi - process converted content (code)
	EvalUrlOri     string // evalUrlori - process original content (URL)
	EvalUrlModi    string // evalUrlmodi - process converted content (URL)
	EvJsOri        string // evJsori - script converter: process original (code)
	EvJsModi       string // evJsmodi - script converter: process converted (code)
	EvUrlOri       string // evUrlori - script converter: process original (URL)
	EvUrlModi      string // evUrlmodi - script converter: process converted (URL)
}

// EvalParamsFromArgs extracts eval parameters from the query args map.
func EvalParamsFromArgs(args map[string]string) EvalParams {
	return EvalParams{
		EvalScriptOri:  args["evalScriptori"],
		EvalScriptModi: args["evalScriptmodi"],
		EvalUrlOri:     args["evalUrlori"],
		EvalUrlModi:    args["evalUrlmodi"],
		EvJsOri:        args["evJsori"],
		EvJsModi:       args["evJsmodi"],
		EvUrlOri:       args["evUrlori"],
		EvUrlModi:      args["evUrlmodi"],
	}
}

// ApplyBeforeConversion applies eval operations to the original content before conversion.
// This handles: evalScriptori, evalUrlori, evJsori, evUrlori
func ApplyBeforeConversion(ctx context.Context, body string, params EvalParams, client *httpclient.Client) string {
	// Inline code eval
	if params.EvalScriptOri != "" {
		ops := ParseEvalCode(params.EvalScriptOri)
		body = ApplyEvalCode(body, ops)
	}
	if params.EvJsOri != "" {
		ops := ParseEvalCode(params.EvJsOri)
		body = ApplyEvalCode(body, ops)
	}

	// URL-based eval: fetch JS code from URL, then parse and apply
	if params.EvalUrlOri != "" {
		code := fetchEvalCode(ctx, params.EvalUrlOri, client)
		if code != "" {
			ops := ParseEvalCode(code)
			body = ApplyEvalCode(body, ops)
		}
	}
	if params.EvUrlOri != "" {
		code := fetchEvalCode(ctx, params.EvUrlOri, client)
		if code != "" {
			ops := ParseEvalCode(code)
			body = ApplyEvalCode(body, ops)
		}
	}

	return body
}

// ApplyAfterConversion applies eval operations to the converted content after conversion.
// This handles: evalScriptmodi, evalUrlmodi, evJsmodi, evUrlmodi
func ApplyAfterConversion(ctx context.Context, body string, params EvalParams, client *httpclient.Client) string {
	if params.EvalScriptModi != "" {
		ops := ParseEvalCode(params.EvalScriptModi)
		body = ApplyEvalCode(body, ops)
	}
	if params.EvJsModi != "" {
		ops := ParseEvalCode(params.EvJsModi)
		body = ApplyEvalCode(body, ops)
	}

	if params.EvalUrlModi != "" {
		code := fetchEvalCode(ctx, params.EvalUrlModi, client)
		if code != "" {
			ops := ParseEvalCode(code)
			body = ApplyEvalCode(body, ops)
		}
	}
	if params.EvUrlModi != "" {
		code := fetchEvalCode(ctx, params.EvUrlModi, client)
		if code != "" {
			ops := ParseEvalCode(code)
			body = ApplyEvalCode(body, ops)
		}
	}

	return body
}

// fetchEvalCode fetches JS code from a URL for eval processing.
func fetchEvalCode(ctx context.Context, urlStr string, client *httpclient.Client) string {
	content, status, err := client.Get(ctx, urlStr)
	if err != nil {
		log.Printf("eval: failed to fetch eval URL %s: %v", urlStr, err)
		return ""
	}
	if status != 200 {
		log.Printf("eval: fetch eval URL %s returned status %d", urlStr, status)
		return ""
	}
	return content
}

// convertJSRegexToGo converts a JS regex pattern with flags to Go regexp format.
func convertJSRegexToGo(pattern, flags string) string {
	// Go uses (?flags) syntax for flags at the start of the pattern
	goFlags := ""
	if strings.Contains(flags, "i") {
		goFlags += "i"
	}
	if strings.Contains(flags, "m") {
		goFlags += "m"
	}
	if strings.Contains(flags, "s") {
		goFlags += "s"
	}

	if goFlags != "" {
		return fmt.Sprintf("(?%s)%s", goFlags, pattern)
	}
	return pattern
}

// unescapeJSString unescapes common JS string escape sequences.
func unescapeJSString(s string) string {
	s = strings.ReplaceAll(s, `\'`, "'")
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\r`, "\r")
	s = strings.ReplaceAll(s, `\t`, "\t")
	return s
}

// truncated returns a truncated version of s with maxLen characters.
func truncated(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
