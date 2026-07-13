// Package frontend provides the minimal conversion UI.
package frontend

import (
	_ "embed"
	"strings"
)

//go:embed index.html
var page string

// GenerateHTML returns the conversion page configured for this server.
func GenerateHTML(baseURL string) string {
	return strings.Replace(page, "__BASE_URL__", strings.TrimRight(baseURL, "/"), 1)
}
