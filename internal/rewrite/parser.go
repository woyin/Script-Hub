// Input: context, log, regexp, strings, internal/config, internal/httpclient, internal/types
// Output: type ParseInput, type ParseOutput, type Parser, 众多解析类型（ParsedRewrite/ParsedModule 等）, func NewParser(), func (Parser) Parse()
// Pos: 业务层-重写解析核心，抓取远程内容并按来源格式解析为统一中间表示
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package rewrite 实现重写规则解析与转换引擎。
// 将 QX 重写、Surge 模块、Loon 插件等格式解析为统一的中间表示，
// 再转换为目标平台格式。对应 JS 版 Rewrite-Parser.js 的完整功能。
package rewrite

import (
	"context"
	"log"
	"regexp"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/types"
)

// ParseInput contains the input parameters for rewrite parsing.
type ParseInput struct {
	URLs       []string
	SourceType string
	TargetApp  string
	Arguments  map[string]string
}

// ParseOutput contains the parsed and converted rewrite output.
type ParseOutput struct {
	Content string
	Headers map[string]string
	Status  int
}

// GetResponse implements the server response interface.
func (o ParseOutput) GetResponse() types.ResponseData {
	return types.ResponseData{
		Status:  o.Status,
		Headers: o.Headers,
		Body:    o.Content,
	}
}

// RewriteType represents the type of rewrite rule.
type RewriteType int

const (
	RewriteTypeRequestHeader RewriteType = iota
	RewriteTypeResponseHeader
	RewriteTypeRequestBody
	RewriteTypeResponseBody
	RewriteTypeEchoResponse
	RewriteTypeReject
	RewriteTypeRejectDict
	RewriteTypeRejectImg
	RewriteTypeRejectTinyGif
	RewriteTypeReject200
	RewriteTypeRejectArray
	RewriteTypeRejectVideo
	RewriteTypeRejectDrop
	RewriteTypeMock
	RewriteTypeMockRequestBody
	RewriteTypeBodyRewrite
	RewriteTypeScript
	RewriteTypeHeaderRewrite
	RewriteTypeHeaderDel
	RewriteTypeHeaderAdd
	RewriteTypeHeaderReplace
	RewriteTypeHeaderReplaceRegex
	RewriteTypeURLRewrite
	RewriteTypeMapLocal
)

// ParsedRewrite represents a parsed rewrite rule from any source format.
type ParsedRewrite struct {
	Pattern              string
	Type                 RewriteType
	Replacement          string            // For QX header/body: "match->replace"; for URL rewrite: target URL; for native header rewrite: full directive
	MatchPart            string            // For QX header/body: the match regex/string (before ->)
	ReplacePart          string            // For QX header/body: the replacement string (after ->)
	EchoCT               string            // For echo-response: content type
	EchoURL              string            // For echo-response: echo data URL
	MockData             string            // Mock inline data
	MockDataPath         string            // Mock data file path
	MockType             string            // data-type (file/text/json/...)
	MockStatus           string            // status-code
	MockHeader           string            // header
	MockBase64           bool              // mock-data-is-base64
	MockIsLoon           bool              // Loon mock-response-body form
	BodyRewrite          *BodyRewriteEntry // non-nil when parsed from body rewrite line
	ScriptPath           string
	ScriptType           string // http-request, http-response, cron, generic
	Arguments            string
	Timeout              int
	RequiresBody         bool
	BodyType             string // request-body, response-body
	CronExp              string // Cron expression for scheduled scripts
	Engine               string // Script engine (Surge-specific: javascript, wasm, etc.)
	MaxSize              string // max-size parameter
	EventName            string // event-name parameter
	BinaryBody           bool   // binary-body-mode
	WakeSystem           bool   // wake-system
	Ability              string // ability parameter
	Enable               bool   // enable parameter
	ScriptUpdateInterval string // script-update-interval
	ImgURL               string // img-url
	Tag                  string // tag (alternative script name source)
	Debug                string // debug parameter
	Desc                 string // desc parameter
	MITM                 []string
}

// ParsedModule represents a fully parsed module/plugin from any platform.
type ParsedModule struct {
	Name               string
	Desc               string
	Rewrites           []ParsedRewrite
	Rules              []string
	Scripts            []ParsedRewrite
	MITM               []string
	Icon               string
	OpenURL            string
	CronExp            string
	Panels             []PanelInfo
	Hosts              []HostInfo
	Category           string
	Keyword            string
	MetaExtra          []string        // extra #!key=value lines (e.g. Loon interactive buttons)
	ModInputBox        []InputBoxEntry // Loon #!select= and #!input= interactive entries
	SgArg              []SgArgument
	BodyRewrites       []BodyRewriteEntry
	ConditionalMITMKey string // set when #!arguments has value=hostname
	SkipProxy          []string
	RealIP             []string
	HNAddMethod        string // %APPEND% or %INSERT% (detected from input)
	FHEAddMethod       string // for force-http-engine-hosts
}

// BodyRewriteEntry holds a parsed body rewrite rule (jq or replace-regex).
type BodyRewriteEntry struct {
	Type  string // http-request, http-response, http-request-jq, http-response-jq
	Regex string
	Value string
}

// SgArgument holds a Surge module #!arguments entry.
type SgArgument struct {
	Key   string
	Value string
	Type  string // input, select, switch
	Tag   string
}

// InputBoxEntry holds a Loon #!select= or #!input= interactive button entry.
// Mirrors JS modInputBox: {a: "select=", b: "value"} → "#!select=value"
type InputBoxEntry struct {
	Key   string // e.g. "select=" or "input="
	Value string
}

// ParseInputBox parses a #!select= or #!input= line into an InputBoxEntry,
// mirroring JS getInputInfo.
func ParseInputBox(line string) *InputBoxEntry {
	line = regexp.MustCompile(`\s*=\s*`).ReplaceAllString(line, "=")
	line = strings.TrimPrefix(line, "#!")
	eqIdx := strings.Index(line, "=")
	if eqIdx < 0 {
		return nil
	}
	return &InputBoxEntry{
		Key:   line[:eqIdx+1],
		Value: line[eqIdx+1:],
	}
}

// PanelInfo holds a Surge [Panel] entry parsed from script-name/title/content/style.
type PanelInfo struct {
	Name        string // first field before the first '='
	Title       string
	Content     string
	Style       string
	ScriptName  string
	UpdateTimer string
	ToggleKey   string // leading {{{key}}} template key for Surge toggle
	Raw         string
}

// HostInfo holds a Surge [Host] entry: domain = value (server/script:...).
type HostInfo struct {
	Domain string
	Value  string
	Raw    string
}

// Parser handles rewrite rule parsing and conversion.
type Parser struct {
	cfg    *config.Config
	client *httpclient.Client
}

// NewParser creates a new rewrite parser.
func NewParser(cfg *config.Config) *Parser {
	return &Parser{
		cfg:    cfg,
		client: httpclient.NewClient(cfg.HTTPTimeout),
	}
}

// Parse fetches remote content and converts rewrite rules to the target format.
func (p *Parser) Parse(ctx context.Context, input ParseInput) (ParseOutput, error) {
	var body string
	localText := input.Arguments["localtext"]

	if len(input.URLs) > 0 {
		if input.URLs[0] == "http://local.text" || input.URLs[0] == "http://local.text/" {
			body = localText
		} else {
			var bodies []string
			reqHeaders := httpclient.ParseCustomHeaders(input.Arguments["headers"])
			for _, u := range input.URLs {
				content, status, err := p.client.GetWithHeaders(ctx, u, reqHeaders)
				if err != nil {
					log.Printf("rewrite fetch error for %s: %v", u, err)
					continue
				}
				if status == 404 {
					bodies = append(bodies, "#!error=404: Not Found")
				} else if status == 200 {
					// Extract content from /* ... */ block comment wrapper (Rewrite-Parser.js L325)
					extracted := extractBlockComment(content)
					bodies = append(bodies, extracted)
				}
			}
			if len(bodies) > 0 {
				body = strings.Join(bodies, "\n\n")
			}
			if localText != "" {
				if body != "" {
					body = body + "\n"
				}
				body = body + localText
			}
		}
	} else {
		body = localText
	}

	if body == "" {
		return ParseOutput{
			Content: "",
			Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Status:  200,
		}, nil
	}

	// Parse based on source type
	var modules []ParsedModule
	switch input.SourceType {
	case config.SourceTypeQXRewrite:
		modules = p.parseQXRewrite(body, input.Arguments)
	case config.SourceTypeSurgeModule:
		modules = p.parseSurgeModule(body, input.Arguments)
	case config.SourceTypeLoonPlugin:
		modules = p.parseLoonPlugin(body, input.Arguments)
	case config.SourceTypeAllModule:
		modules = p.parseAutoDetect(body, input.Arguments)
	default:
		modules = p.parseAutoDetect(body, input.Arguments)
	}

	// Apply parameter modifications to parsed modules
	for i := range modules {
		// Apply metadata overrides (name, desc, icon)
		ApplyMetadataOverrides(&modules[i], input.Arguments)

		// Check for conditional MITM (#!arguments with value=hostname)
		for _, arg := range modules[i].SgArg {
			if arg.Value == "hostname" {
				modules[i].ConditionalMITMKey = "{{{" + arg.Key + "}}}"
				break
			}
		}

		// Apply icon replacement / KeLee icon name resolution
		ApplyIconReplacement(ctx, &modules[i], input.Arguments, p.client,
			strings.Contains(input.TargetApp, "stash") || strings.Contains(input.TargetApp, "loon"))

		// Apply script-level modifications
		modules[i].Scripts = ApplyArgModification(modules[i].Scripts, input.Arguments["arg"], input.Arguments["argv"])
		modules[i].Scripts = ApplyScriptNameModification(modules[i].Scripts, input.Arguments["njsnametarget"], input.Arguments["njsname"])
		modules[i].Scripts = ApplyTimeoutModification(modules[i].Scripts, input.Arguments["timeoutt"], input.Arguments["timeoutv"])
		modules[i].Scripts = ApplyEngineModification(modules[i].Scripts, input.Arguments["enginet"], input.Arguments["enginev"])
		modules[i].Scripts = ApplyCronModification(modules[i].Scripts, input.Arguments["cron"], input.Arguments["cronexp"])

		// Apply MITM modifications
		modules[i].MITM = ApplyMITMAdditions(modules[i].MITM, input.Arguments["hnadd"])
		modules[i].MITM = ApplyMITMDeletions(modules[i].MITM, input.Arguments["hndel"])
		modules[i].MITM = ApplyMITNRegexDeletions(modules[i].MITM, input.Arguments["hnregdel"])

		// Apply policy to rules
		modules[i].Rules = ApplyPolicyToRules(modules[i].Rules, input.Arguments["policy"])
		modules[i].Rules = ApplySniPm(modules[i].Rules, input.Arguments["sni"], input.Arguments["pm"])

		// Deduplicate rewrites and scripts (matches JS rwBox/jsBox dedup)
		modules[i].Rewrites = dedupRewrites(modules[i].Rewrites)
		modules[i].Scripts = dedupScripts(modules[i].Scripts)

		// Deduplicate MITM
		modules[i].MITM = uniqueStrings(modules[i].MITM)
	}

	// Convert to target format
	output := p.convertModules(modules, input.TargetApp, input.Arguments)

	return ParseOutput{
		Content: output,
		Headers: map[string]string{
			"Content-Type":                 "text/plain; charset=utf-8",
			"Access-Control-Allow-Origin":  "*",
			"Access-Control-Allow-Methods": "POST,GET,OPTIONS,PUT,DELETE",
			"Access-Control-Allow-Headers": "Origin, X-Requested-With, Content-Type, Accept",
		},
		Status: 200,
	}, nil
}

// extractBlockComment extracts content from a /* ... */ block comment wrapper,
// matching Rewrite-Parser.js behavior (L325): if the body starts with a block
// comment, return its inner content; otherwise return the body as-is.
func extractBlockComment(body string) string {
	re := regexp.MustCompile(`(?s)^\s*/\*.*?[\r\n]\s*\*+/`)
	if re.MatchString(body) {
		inner := regexp.MustCompile(`(?s)^(?:\n|\r)*/\*([\s\S]*?)(?:\r|\n)\s*\*+/`)
		if m := inner.FindStringSubmatch(body); len(m) >= 2 {
			return m[1]
		}
	}
	return body
}
