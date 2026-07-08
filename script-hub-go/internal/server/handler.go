package server

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/converter"
	"github.com/script-hub-org/script-hub/internal/frontend"
	"github.com/script-hub-org/script-hub/internal/rewrite"
	"github.com/script-hub-org/script-hub/internal/rule"
	"github.com/script-hub-org/script-hub/internal/types"
)

func (s *Server) scriptHubHandler(w http.ResponseWriter, r *http.Request) {
	scriptURL := buildScriptHubURL(r)
	log.Printf("scriptHubHandler url: %s", scriptURL)

	if strings.Contains(scriptURL, "/reload") {
		s.reloadHandler(w, r)
		return
	}

	baseURL := s.cfg.BaseURL

	html := frontend.GenerateHTML(baseURL)
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(html))
}

func (s *Server) reloadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(`<meta charset="UTF-8" /><h1>✅ Surge 重载完成</h1><a href="surge://">点此打开 Surge</a>`))
}

func (s *Server) rewriteParserHandler(w http.ResponseWriter, r *http.Request) {
	scriptURL := buildScriptHubURL(r)
	log.Printf("rewriteParserHandler url: %s", scriptURL)

	req, _ := extractReqFromURL(scriptURL)
	urlArg := extractURLArg(scriptURL)
	queryParams := parseQueryString(urlArg)

	parser := rewrite.NewParser(s.cfg)
	input := rewrite.ParseInput{
		URLs:       decodeReqArr(req),
		SourceType: queryParams["type"],
		TargetApp:  queryParams["target"],
		Arguments:  queryParams,
	}

	output, err := parser.Parse(r.Context(), input)
	if err != nil {
		log.Printf("rewriteParser error: %v", err)
		http.Error(w, fmt.Sprintf("Rewrite parse error: %v", err), http.StatusInternalServerError)
		return
	}

	writeResponse(w, output, s.cfg)
}

func (s *Server) ruleParserHandler(w http.ResponseWriter, r *http.Request) {
	scriptURL := buildScriptHubURL(r)
	log.Printf("ruleParserHandler url: %s", scriptURL)

	req, _ := extractReqFromURL(scriptURL)
	urlArg := extractURLArg(scriptURL)
	queryParams := parseQueryString(urlArg)

	parser := rule.NewParser(s.cfg)
	input := rule.ParseInput{
		URLs:      decodeReqArr(req),
		TargetApp: queryParams["target"],
		Arguments: queryParams,
	}

	output, err := parser.Parse(r.Context(), input)
	if err != nil {
		log.Printf("ruleParser error: %v", err)
		http.Error(w, fmt.Sprintf("Rule parse error: %v", err), http.StatusInternalServerError)
		return
	}

	writeResponse(w, output, s.cfg)
}

func (s *Server) scriptConverterHandler(w http.ResponseWriter, r *http.Request) {
	scriptURL := buildScriptHubURL(r)
	log.Printf("scriptConverterHandler url: %s", scriptURL)

	req := extractConvertReq(scriptURL)
	urlArg := extractURLArg(scriptURL)
	queryParams := parseQueryString(urlArg)

	// For /convert/ URLs without /_end_/, query params are embedded in the URL itself
	if len(queryParams) == 0 {
		if qIdx := strings.Index(req, "?"); qIdx >= 0 {
			queryParams = parseQueryString(req[qIdx:])
			req = req[:qIdx]
		}
	}

	conv := converter.NewConverter(s.cfg)
	input := converter.ConvertInput{
		URL:        req,
		LocalText:  queryParams["localtext"],
		SourceType: queryParams["type"],
		TargetApp:  queryParams["target-app"],
		Arguments:  queryParams,
		KeepHeader: queryParams["keepHeader"] == "true",
		JsDelivr:   queryParams["jsDelivr"],
	}

	output, err := conv.Convert(r.Context(), input)
	if err != nil {
		log.Printf("scriptConverter error: %v", err)
		http.Error(w, fmt.Sprintf("Script convert error: %v", err), http.StatusInternalServerError)
		return
	}

	writeResponse(w, output, s.cfg)
}

func writeResponse(w http.ResponseWriter, output types.ResponseWriter, cfg *config.Config) {
	resp := output.GetResponse()
	baseURL := cfg.BaseURL

	w.WriteHeader(resp.Status)
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	body := resp.Body
	if strings.Contains(body, "https://script.hub/") {
		body = strings.ReplaceAll(body, "https://script.hub/", baseURL+"/")
		body = strings.ReplaceAll(body, "http://script.hub/", baseURL+"/")
	}
	w.Write([]byte(body))
}

func parseQueryString(query string) map[string]string {
	result := make(map[string]string)
	if query == "" {
		return result
	}
	query = strings.TrimPrefix(query, "?")
	if idx := strings.Index(query, "?"); idx >= 0 {
		query = query[idx+1:]
	}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		key, _ := url.QueryUnescape(kv[0])
		if len(kv) == 2 {
			val, _ := url.QueryUnescape(kv[1])
			result[key] = val
		} else {
			result[key] = ""
		}
	}
	return result
}

func decodeReqArr(req string) []string {
	if strings.Contains(req, "%F0%9F%98%82") {
		parts := strings.Split(req, "%F0%9F%98%82")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			decoded, err := url.QueryUnescape(p)
			if err != nil {
				decoded = p
			}
			result = append(result, decoded)
		}
		return result
	}
	decoded, err := url.QueryUnescape(req)
	if err != nil {
		decoded = req
	}
	return []string{decoded}
}
