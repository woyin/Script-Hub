package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validRefreshJob() RefreshJob {
	return RefreshJob{ID: "china", Enabled: true, IntervalSeconds: 21600, Request: RefreshRequest{Type: "rule-set", Target: "surge-rule-set", URLs: []string{"https://example.com/rules.yaml"}, Args: map[string]string{"del": "true"}}}
}
func writeRefreshFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestLoadRefreshJobsValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RefreshJob)
		ttl    int
		bad    bool
	}{
		{"valid", func(j *RefreshJob) {}, 86400, false},
		{"id", func(j *RefreshJob) { j.ID = "bad/name" }, 86400, true},
		{"interval", func(j *RefreshJob) { j.IntervalSeconds = 0 }, 86400, true},
		{"ttl", func(j *RefreshJob) {}, 21600, true},
		{"cache-disabled", func(j *RefreshJob) {}, 0, true},
		{"disabled", func(j *RefreshJob) { j.Enabled = false }, 0, false},
		{"target", func(j *RefreshJob) { j.Request.Target = "rule-set" }, 86400, true},
		{"type", func(j *RefreshJob) { j.Request.Type = "qx-rewrite" }, 86400, true},
		{"scheme", func(j *RefreshJob) { j.Request.URLs = []string{"file:///etc/passwd"} }, 86400, true},
		{"userinfo", func(j *RefreshJob) { j.Request.URLs = []string{"https://user:secret@example.com/file"} }, 86400, true},
		{"reserved", func(j *RefreshJob) { j.Request.Args["refresh"] = "true" }, 86400, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := validRefreshJob()
			test.mutate(&job)
			data, _ := json.Marshal(RefreshDocument{RefreshJobs: []RefreshJob{job}})
			_, err := LoadRefreshJobs(writeRefreshFile(t, data), test.ttl)
			if (err != nil) != test.bad {
				t.Fatalf("error=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("credentials leaked")
			}
		})
	}
}
func TestLoadRefreshJobsStrictJSON(t *testing.T) {
	for _, data := range []string{`{}`, `{"refreshJobs":null}`, `{"refreshJobs":[],"unknown":1}`, `{"refreshJobs":[]} {}`, `{"refreshJobs":[{"id":"x","enabled":true,"intervalSeconds":60,"request":{"type":"rule-set","target":"surge-rule-set","urls":["https://example.com"],"args":{"del":true}}}]}`} {
		if _, err := LoadRefreshJobs(writeRefreshFile(t, []byte(data)), 86400); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	a := validRefreshJob()
	b := validRefreshJob()
	b.ID = "different"
	data, _ := json.Marshal(RefreshDocument{RefreshJobs: []RefreshJob{a, b}})
	if _, err := LoadRefreshJobs(writeRefreshFile(t, data), 86400); err == nil {
		t.Fatal("duplicate conversion accepted")
	}
	b.Request.URLs = []string{"https://other.example.com"}
	b.ID = a.ID
	data, _ = json.Marshal(RefreshDocument{RefreshJobs: []RefreshJob{a, b}})
	if _, err := LoadRefreshJobs(writeRefreshFile(t, data), 86400); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	if _, err := LoadRefreshJobs(writeRefreshFile(t, []byte(`{"refreshJobs":[]}`)), 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRefreshJobs("", 0); err != nil {
		t.Fatal(err)
	}
}
