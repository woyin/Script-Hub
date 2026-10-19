package rule

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/script-hub-org/script-hub/internal/cache"
	"github.com/script-hub-org/script-hub/internal/config"
)

func TestStreamMatchesLegacy(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.CacheDir = t.TempDir()
	cfg.MaxBodyKB = 1024
	disk, err := cache.New(cfg.CacheDir, 86400, 0)
	if err != nil {
		t.Fatal(err)
	}
	parser := NewParser(cfg)
	inputs := []string{"", "\n", "# comment", "payload:\n  - '+.example.com'\n  - 'DOMAIN-SUFFIX,foo.example'\n  - 'IP-CIDR,1.2.3.0/24'\n  - 'IP-CIDR6,::1/128'", "DOMAIN,one.example\r\nDOMAIN,one.example\r\nDOMAIN,two.example", "#DOMAIN,include.example\nDOMAIN,exclude.example\nPROCESS-NAME,/usr/bin/test\nDST-PORT,443\nOR,((DOMAIN,a),(DOMAIN,b))\nHO-ST-WILDCARD,*.test", "URL-REGEX,^https://test/a{1,3}\nDOMAIN-REGEX,xxDOMAIN,abc\nIP-CIDR,1.2.3.4/32,no-resolve", strings.Repeat("a", 70<<10) + ".example"}
	targets := []string{"surge-rule-set", "loon-rule-set", "stash-rule-set", "shadowrocket-rule-set", "egern-rule-set", "lancex-rule-set", "surge-domain-set", "surge-domain-set2", "stash-domain-set", "stash-domain-set2"}
	options := []map[string]string{nil, {"x": "exclude"}, {"y": "include"}, {"nore": "true", "policy": "DIRECT", "sni": "example", "pm": "example"}}
	for i, body := range inputs {
		for _, target := range targets {
			for j, args := range options {
				t.Run(fmt.Sprintf("%d/%s/%d", i, target, j), func(t *testing.T) {
					params := map[string]string{"localtext": body}
					for k, v := range args {
						params[k] = v
					}
					input := ParseInput{TargetApp: target, Arguments: params}
					old, err := parser.Parse(context.Background(), input)
					if err != nil {
						t.Fatal(err)
					}
					ws, err := disk.Workspace("")
					if err != nil {
						t.Fatal(err)
					}
					got, err := parser.ParseStream(context.Background(), input, ws)
					if err != nil {
						t.Fatal(err)
					}
					defer got.File.Close()
					data, err := io.ReadAll(got.File.Reader())
					if err != nil {
						t.Fatal(err)
					}
					if string(data) != old.Content {
						t.Fatalf("stream=%q\nlegacy=%q", data, old.Content)
					}
					if got.Incomplete != old.Incomplete {
						t.Fatal("incomplete mismatch")
					}
				})
			}
		}
	}
}

func TestStreamDiscardsInterruptedSource(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.CacheDir = t.TempDir()
	disk, err := cache.New(cfg.CacheDir, 86400, 0)
	if err != nil {
		t.Fatal(err)
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, "DOMAIN,partial.example\n")
	}))
	defer source.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "payload:\n - DOMAIN,complete.example") }))
	defer good.Close()
	ws, err := disk.Workspace("")
	if err != nil {
		t.Fatal(err)
	}
	out, err := NewParser(cfg).ParseStream(context.Background(), ParseInput{URLs: []string{source.URL, good.URL}, TargetApp: "surge-rule-set"}, ws)
	if err != nil {
		t.Fatal(err)
	}
	defer out.File.Close()
	data, _ := io.ReadAll(out.File.Reader())
	if !out.Incomplete || strings.Contains(string(data), "partial.example") || !strings.Contains(string(data), "complete.example") {
		t.Fatalf("invalid partial result %s", data)
	}
}

func TestStreamMultiSourceOrderAndCancellation(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.CacheDir = t.TempDir()
	disk, _ := cache.New(cfg.CacheDir, 86400, 0)
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "DOMAIN,first.example") }))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "DOMAIN,second.example") }))
	defer b.Close()
	parser := NewParser(cfg)
	input := ParseInput{URLs: []string{a.URL, b.URL}, TargetApp: "stash-rule-set", Arguments: map[string]string{"localtext": "DOMAIN,last.example"}}
	old, _ := parser.Parse(context.Background(), input)
	ws, _ := disk.Workspace("")
	out, err := parser.ParseStream(context.Background(), input, ws)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(out.File.Reader())
	out.File.Close()
	if string(data) != old.Content {
		t.Fatalf("multi-source mismatch: %s", data)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ws, _ = disk.Workspace("")
	if _, err := parser.ParseStream(ctx, input, ws); err != context.Canceled {
		t.Fatalf("cancel err=%v", err)
	}
}
