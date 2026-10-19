// Input: encoding/json, os, net/url
// Output: RefreshRequest, RefreshJob, LoadRefreshJobs
// Pos: 配置层-后台规则刷新清单的严格读取和验证
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

type RefreshRequest struct {
	URLs   []string          `json:"urls,omitempty"`
	Type   string            `json:"type"`
	Target string            `json:"target,omitempty"`
	Args   map[string]string `json:"args,omitempty"`
}
type RefreshJob struct {
	ID              string         `json:"id"`
	Enabled         bool           `json:"enabled"`
	IntervalSeconds int            `json:"intervalSeconds"`
	Request         RefreshRequest `json:"request"`
}
type RefreshDocument struct {
	RefreshJobs []RefreshJob `json:"refreshJobs"`
}

func LoadRefreshJobs(path string, cacheTTL int) ([]RefreshJob, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open refresh jobs file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read refresh jobs file")
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("refresh jobs file exceeds 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var doc RefreshDocument
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid refresh jobs JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("refresh jobs file must contain exactly one JSON document")
	}
	if doc.RefreshJobs == nil {
		return nil, fmt.Errorf("refreshJobs must be an array")
	}
	if len(doc.RefreshJobs) > 128 {
		return nil, fmt.Errorf("at most 128 refresh jobs are allowed")
	}
	ids := map[string]bool{}
	requests := map[string]bool{}
	validID := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	targets := map[string]bool{}
	for _, target := range []string{TargetSurgeRuleSet, TargetLoonRuleSet, TargetStashRuleSet, TargetShadowrocketRuleSet, TargetEgernRuleSet, TargetLanceXRuleSet, TargetSurgeDomainSet, TargetSurgeDomainSet2, TargetStashDomainSet, TargetStashDomainSet2} {
		targets[target] = true
	}
	for i, job := range doc.RefreshJobs {
		fail := func(reason string) ([]RefreshJob, error) { return nil, fmt.Errorf("refreshJobs[%d]: %s", i, reason) }
		if !validID.MatchString(job.ID) {
			return fail("id must contain 1-64 letters, digits, hyphens or underscores and start with a letter or digit")
		}
		if ids[job.ID] {
			return fail("duplicate id")
		}
		ids[job.ID] = true
		if job.IntervalSeconds < 60 || job.IntervalSeconds > 604800 {
			return fail("intervalSeconds must be between 60 and 604800")
		}
		jitterSeconds := job.IntervalSeconds / 10
		if jitterSeconds > 30 {
			jitterSeconds = 30
		}
		if job.Enabled && (cacheTTL <= 0 || job.IntervalSeconds+jitterSeconds >= cacheTTL) {
			return fail("enabled jobs require cache TTL greater than intervalSeconds plus scheduling jitter (up to 30 seconds)")
		}
		req := job.Request
		if req.Type != SourceTypeRuleSet {
			return fail("request.type must be rule-set")
		}
		if !targets[req.Target] {
			return fail("request.target must be an explicit supported rule-set or domain-set target")
		}
		if len(req.URLs) == 0 || len(req.URLs) > 16 {
			return fail("request.urls must contain 1-16 HTTP(S) URLs")
		}
		for _, raw := range req.URLs {
			parsed, err := url.Parse(raw)
			if err != nil || len(raw) > 8192 || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
				return fail("request.urls contains an invalid HTTP(S) URL; credentials and fragments are not allowed")
			}
		}
		for _, key := range []string{"localtext", "refresh", "type", "target"} {
			if _, ok := req.Args[key]; ok {
				return fail("request.args must not contain localtext, refresh, type or target")
			}
		}
		if job.Enabled {
			canonical, _ := json.Marshal(req)
			if requests[string(canonical)] {
				return fail("duplicate enabled conversion request")
			}
			requests[string(canonical)] = true
		}
	}
	return doc.RefreshJobs, nil
}
