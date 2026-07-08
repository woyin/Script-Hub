package converter

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/types"
)

// ConvertInput contains the input parameters for script conversion.
type ConvertInput struct {
	URL            string
	LocalText      string
	SourceType     string
	TargetApp      string
	Arguments      map[string]string
	KeepHeader     bool
	JsDelivr       string
	RequestHeaders http.Header
}

// ConvertOutput contains the converted script output.
type ConvertOutput struct {
	Content     string
	Headers     map[string]string
	Status      int
	MITM        []string
	ContentType string
}

// GetResponse implements the server.ResponseWriter interface.
func (o ConvertOutput) GetResponse() types.ResponseData {
	return types.ResponseData{
		Status:  o.Status,
		Headers: o.Headers,
		Body:    o.Content,
	}
}

// Converter handles QX script to Surge/Loon script conversion.
type Converter struct {
	cfg    *config.Config
	client *httpclient.Client
}

// NewConverter creates a new script converter.
func NewConverter(cfg *config.Config) *Converter {
	return &Converter{
		cfg:    cfg,
		client: httpclient.NewClient(cfg.HTTPTimeout),
	}
}

// Convert fetches and converts a QX script to the target app format.
func (c *Converter) Convert(ctx context.Context, input ConvertInput) (ConvertOutput, error) {
	var scriptContent string
	var err error

	// Fetch script content
	if input.URL != "" && !strings.HasPrefix(input.URL, "http://local.text") {
		scriptContent, _, err = c.client.Get(ctx, input.URL)
		if err != nil {
			return ConvertOutput{
				Content: "Script fetch error: " + err.Error(),
				Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
				Status:  500,
			}, err
		}
	} else {
		scriptContent = input.LocalText
	}

	if scriptContent == "" {
		return ConvertOutput{
			Content: "",
			Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Status:  200,
		}, nil
	}

	// Convert based on target app
	targetApp := strings.ToLower(input.TargetApp)
	var converted string

	switch {
	case strings.Contains(targetApp, "surge") || strings.Contains(targetApp, "shadowrocket"):
		converted = c.convertQXToSurge(scriptContent, input)
	case strings.Contains(targetApp, "loon"):
		converted = c.convertQXToLoon(scriptContent, input)
	default:
		converted = scriptContent
	}

	return ConvertOutput{
		Content: converted,
		Headers: map[string]string{
			"Content-Type":                "text/plain; charset=utf-8",
			"Access-Control-Allow-Origin":  "*",
			"Access-Control-Allow-Methods": "POST,GET,OPTIONS,PUT,DELETE",
			"Access-Control-Allow-Headers": "Origin, X-Requested-With, Content-Type, Accept",
		},
		Status:      200,
		ContentType: "text/plain; charset=utf-8",
	}, nil
}

// convertQXToSurge converts QX script syntax to Surge compatible syntax.
func (c *Converter) convertQXToSurge(script string, input ConvertInput) string {
	result := script

	// Add compatibility wrapper at the start
	wrapper := generateSurgeCompatWrapper(input)
	result = wrapper + result

	// QX API replacements
	result = strings.ReplaceAll(result, "$notify(", "$notification.post(")
	result = strings.ReplaceAll(result, "$prefs.get(", "$persistentStore.read(")
	result = strings.ReplaceAll(result, "$prefs.set(", "$persistentStore.write(")
	result = strings.ReplaceAll(result, "$prefs.remove(", "// $prefs.remove(")

	// Handle $done - keep as is for Surge
	// Handle $task - needs wrapper
	result = strings.ReplaceAll(result, "$task.fetch(", "$http.get(")

	// Handle require() - log warning
	if strings.Contains(result, "require(") {
		log.Println("Warning: require() calls need manual conversion")
	}

	return result
}

// convertQXToLoon converts QX script syntax to Loon compatible syntax.
func (c *Converter) convertQXToLoon(script string, input ConvertInput) string {
	result := script

	// Add compatibility wrapper
	wrapper := generateLoonCompatWrapper(input)
	result = wrapper + result

	// QX API replacements for Loon
	result = strings.ReplaceAll(result, "$notify(", "$notification.post(")
	result = strings.ReplaceAll(result, "$prefs.get(", "$persistentStore.read(")
	result = strings.ReplaceAll(result, "$prefs.set(", "$persistentStore.write(")

	return result
}

// generateSurgeCompatWrapper generates a compatibility wrapper for QX → Surge.
func generateSurgeCompatWrapper(input ConvertInput) string {
	var sb strings.Builder
	sb.WriteString("// Auto-generated compatibility wrapper by Script Hub (Go)\n")
	sb.WriteString("// QX → Surge compatibility layer\n\n")

	// Argument injection
	if argStr, ok := input.Arguments["argument"]; ok && argStr != "" {
		sb.WriteString("var $argument = \"" + argStr + "\";\n\n")
	}

	return sb.String()
}

// generateLoonCompatWrapper generates a compatibility wrapper for QX → Loon.
func generateLoonCompatWrapper(input ConvertInput) string {
	var sb strings.Builder
	sb.WriteString("// Auto-generated compatibility wrapper by Script Hub (Go)\n")
	sb.WriteString("// QX → Loon compatibility layer\n\n")

	if argStr, ok := input.Arguments["argument"]; ok && argStr != "" {
		sb.WriteString("var $argument = \"" + argStr + "\";\n\n")
	}

	return sb.String()
}
