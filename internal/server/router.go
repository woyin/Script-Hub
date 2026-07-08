package server

import (
	"net/http"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
)

// setupRoutes uses a catch-all handler with manual URL dispatching,
// matching the original scriptMap.js pattern-based routing logic.
// Chi's pattern matcher cannot handle URLs with `://` in path segments.
func (s *Server) setupRoutes() {
	s.router.Get("/*", s.dispatchHandler)
}

// dispatchHandler implements the same routing logic as the original scriptMap.js.
func (s *Server) dispatchHandler(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.RequestURI()

	switch {
	case uri == "/" || strings.HasPrefix(uri, "/edit/") || uri == "/reload":
		s.scriptHubHandler(w, r)

	case strings.Contains(uri, "/file/_start_/"):
		s.fileHandler(w, r)

	case strings.Contains(uri, "/convert/"):
		s.scriptConverterHandler(w, r)

	default:
		http.NotFound(w, r)
	}
}

// fileHandler dispatches between rewrite parser and rule parser
// based on the "type" query parameter.
func (s *Server) fileHandler(w http.ResponseWriter, r *http.Request) {
	queryType := r.URL.Query().Get("type")
	switch {
	case queryType == config.SourceTypeQXRewrite,
		queryType == config.SourceTypeSurgeModule,
		queryType == config.SourceTypeLoonPlugin,
		queryType == config.SourceTypeAllModule:
		s.rewriteParserHandler(w, r)
	case queryType == config.SourceTypeRuleSet:
		s.ruleParserHandler(w, r)
	default:
		http.Error(w, "Unknown type parameter", http.StatusBadRequest)
	}
}

// buildScriptHubURL constructs the internal script.hub URL from the request.
func buildScriptHubURL(r *http.Request) string {
	return "http://script.hub" + r.URL.RequestURI()
}

// extractReqFromURL extracts the URL-encoded request path between /file/_start_/ and /_end_/ .
func extractReqFromURL(rawURL string) (string, []string) {
	parts := strings.SplitN(rawURL, "/file/_start_/", 2)
	if len(parts) < 2 {
		return "", nil
	}
	rest := parts[1]
	endParts := strings.SplitN(rest, "/_end_/", 2)
	if len(endParts) < 1 {
		return "", nil
	}
	req := endParts[0]

	if strings.Contains(req, "%F0%9F%98%82") {
		return req, strings.Split(req, "%F0%9F%98%82")
	}
	return req, []string{req}
}

// extractURLArg extracts the part after /_end_/ from the URL.
func extractURLArg(rawURL string) string {
	parts := strings.SplitN(rawURL, "/_end_/", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// extractConvertReq extracts the URL-encoded path from /convert/ URLs.
func extractConvertReq(rawURL string) string {
	parts := strings.SplitN(rawURL, "/convert/", 2)
	if len(parts) < 2 {
		return ""
	}
	rest := parts[1]
	rest = strings.TrimPrefix(rest, "_start_/")
	endParts := strings.SplitN(rest, "/_end_/", 2)
	if len(endParts) < 1 {
		return rest
	}
	return endParts[0]
}
