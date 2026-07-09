// Package eval 实现 JS eval 代码的解析与执行。
// 支持 body.replace() 和 body.split().join() 模式的快速路径解析，
// 以及通过 goja 运行时的任意 JS 代码回退执行。
// 对应 JS 版 Rewrite-Parser.js 和 script-converter.js 中的 eval 逻辑。
package eval

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/dop251/goja"
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
// Returns operations and a boolean indicating if all code was fully handled
// by pattern matching (false means goja fallback is needed).
func ParseEvalCode(code string) ([]Operation, bool) {
	if code == "" {
		return nil, true
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
	fullyHandled := stripped == ""
	if !fullyHandled {
		log.Printf("eval: some JS code will use goja fallback: %s", truncated(stripped, 200))
	}

	return ops, fullyHandled
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
		ops, fullyHandled := ParseEvalCode(params.EvalScriptOri)
		if fullyHandled {
			body = ApplyEvalCode(body, ops)
		} else {
			body = ApplyEvalArbitrary(body, params.EvalScriptOri)
		}
	}
	if params.EvJsOri != "" {
		ops, fullyHandled := ParseEvalCode(params.EvJsOri)
		if fullyHandled {
			body = ApplyEvalCode(body, ops)
		} else {
			body = ApplyEvalArbitrary(body, params.EvJsOri)
		}
	}

	// URL-based eval: fetch JS code from URL, then parse and apply
	if params.EvalUrlOri != "" {
		code := fetchEvalCode(ctx, params.EvalUrlOri, client)
		if code != "" {
			ops, fullyHandled := ParseEvalCode(code)
			if fullyHandled {
				body = ApplyEvalCode(body, ops)
			} else {
				body = ApplyEvalArbitrary(body, code)
			}
		}
	}
	if params.EvUrlOri != "" {
		code := fetchEvalCode(ctx, params.EvUrlOri, client)
		if code != "" {
			ops, fullyHandled := ParseEvalCode(code)
			if fullyHandled {
				body = ApplyEvalCode(body, ops)
			} else {
				body = ApplyEvalArbitrary(body, code)
			}
		}
	}

	return body
}

// ApplyAfterConversion applies eval operations to the converted content after conversion.
// This handles: evalScriptmodi, evalUrlmodi, evJsmodi, evUrlmodi
func ApplyAfterConversion(ctx context.Context, body string, params EvalParams, client *httpclient.Client) string {
	if params.EvalScriptModi != "" {
		ops, fullyHandled := ParseEvalCode(params.EvalScriptModi)
		if fullyHandled {
			body = ApplyEvalCode(body, ops)
		} else {
			body = ApplyEvalArbitrary(body, params.EvalScriptModi)
		}
	}
	if params.EvJsModi != "" {
		ops, fullyHandled := ParseEvalCode(params.EvJsModi)
		if fullyHandled {
			body = ApplyEvalCode(body, ops)
		} else {
			body = ApplyEvalArbitrary(body, params.EvJsModi)
		}
	}

	if params.EvalUrlModi != "" {
		code := fetchEvalCode(ctx, params.EvalUrlModi, client)
		if code != "" {
			ops, fullyHandled := ParseEvalCode(code)
			if fullyHandled {
				body = ApplyEvalCode(body, ops)
			} else {
				body = ApplyEvalArbitrary(body, code)
			}
		}
	}
	if params.EvUrlModi != "" {
		code := fetchEvalCode(ctx, params.EvUrlModi, client)
		if code != "" {
			ops, fullyHandled := ParseEvalCode(code)
			if fullyHandled {
				body = ApplyEvalCode(body, ops)
			} else {
				body = ApplyEvalArbitrary(body, code)
			}
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

// ApplyEvalArbitrary executes arbitrary JS eval code using the goja runtime.
// This is the fallback for code that can't be parsed by pattern matching.
// The JS code has access to a `body` variable that it can modify.
func ApplyEvalArbitrary(body, code string) string {
	if code == "" {
		return body
	}
	vm := goja.New()
	if err := vm.Set("body", body); err != nil {
		log.Printf("eval: failed to set body in goja VM: %v", err)
		return body
	}
	// Provide console.log as a no-op to avoid errors from JS console calls
	console := vm.NewObject()
	console.Set("log", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	console.Set("error", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	console.Set("warn", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	vm.Set("console", console)

	result, err := vm.RunString(code)
	if err != nil {
		log.Printf("eval: arbitrary JS eval error: %v", err)
		return body
	}
	_ = result
	// Read back the body variable (JS may have reassigned it)
	bodyVal := vm.Get("body")
	if bodyVal == nil || goja.IsUndefined(bodyVal) || goja.IsNull(bodyVal) {
		return body
	}
	return bodyVal.String()
}
