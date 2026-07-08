package rewrite

import (
	"context"
	"log"
	"net/http"
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
	Headers    http.Header
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
	RewriteTypeRequestHeader  RewriteType = iota
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
	RewriteTypeScript
	RewriteTypeHeaderRewrite
	RewriteTypeURLRewrite
	RewriteTypeMapLocal
)

// ParsedRewrite represents a parsed rewrite rule from any source format.
type ParsedRewrite struct {
	Pattern      string
	Type         RewriteType
	Replacement  string // For QX header/body: "match->replace"; for URL rewrite: target URL; for native header rewrite: full directive
	MatchPart    string // For QX header/body: the match regex/string (before ->)
	ReplacePart  string // For QX header/body: the replacement string (after ->)
	EchoCT       string // For echo-response: content type
	EchoURL      string // For echo-response: echo data URL
	ScriptPath   string
	ScriptType   string // http-request, http-response
	Arguments    string
	Timeout      int
	RequiresBody bool
	BodyType     string // request-body, response-body
	MITM         []string
}

// ParsedModule represents a fully parsed module/plugin from any platform.
type ParsedModule struct {
	Name     string
	Desc     string
	Rewrites []ParsedRewrite
	Rules    []string
	Scripts  []ParsedRewrite
	MITM     []string
	Icon     string
	OpenURL  string
	CronExp  string
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
			for _, u := range input.URLs {
				content, status, err := p.client.Get(ctx, u)
				if err != nil {
					log.Printf("rewrite fetch error for %s: %v", u, err)
					continue
				}
				if status == 404 {
					bodies = append(bodies, "#!error=404: Not Found")
				} else if status == 200 {
					bodies = append(bodies, content)
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

	// Convert to target format
	output := p.convertModules(modules, input.TargetApp, input.Arguments)

	return ParseOutput{
		Content: output,
		Headers: map[string]string{
			"Content-Type":                "text/plain; charset=utf-8",
			"Access-Control-Allow-Origin":  "*",
			"Access-Control-Allow-Methods": "POST,GET,OPTIONS,PUT,DELETE",
			"Access-Control-Allow-Headers": "Origin, X-Requested-With, Content-Type, Accept",
		},
		Status: 200,
	}, nil
}
