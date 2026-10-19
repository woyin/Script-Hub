package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/manager"
	"github.com/script-hub-org/script-hub/internal/ssrf"
)

const managerTestKey = "abcdefghijklmnopqrstuvwxyz0123456789abcdef"

func managerConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.LoadConfig()
	root := t.TempDir()
	cfg.CacheDir = filepath.Join(root, "cache")
	cfg.ManagerDataDir = filepath.Join(root, "manager")
	cfg.ManagerBackendKey = managerTestKey
	cfg.RefreshJobs = nil
	return cfg
}
func TestManagementDisabledAndBadKeyRefusesStartup(t *testing.T) {
	cfg := managerConfig(t)
	cfg.ManagerBackendKey = ""
	srv := New(cfg)
	defer srv.Shutdown(context.Background())
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest("GET", "/manage/"+managerTestKey+"/", nil))
	if rec.Code != 404 {
		t.Fatal("management unexpectedly enabled")
	}
	cfg = managerConfig(t)
	cfg.ManagerBackendKey = "weak"
	bad := New(cfg)
	defer bad.Shutdown(context.Background())
	if err := bad.Start("127.0.0.1:0"); err == nil {
		t.Fatal("invalid management configuration started")
	}
}
func TestManagementServerWiringAndSSRF(t *testing.T) {
	old := ssrf.Enabled
	ssrf.Enabled = true
	defer func() { ssrf.Enabled = old }()
	var hits atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("PRIVATE-CONTENT")) }))
	defer source.Close()
	srv := New(managerConfig(t))
	defer srv.Shutdown(context.Background())
	if srv.startupErr != nil {
		t.Fatal(srv.startupErr)
	}
	spec := manager.Spec{Name: "blocked private source", Source: "remote", URLs: []string{source.URL}}
	b, _ := json.Marshal(spec)
	req := httptest.NewRequest("POST", "/manage/"+managerTestKey+"/api/files", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var saved struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &saved)
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest("GET", "/manage/"+managerTestKey+"/api/files/"+saved.ID+"/export", nil))
	if rec.Code != 422 || hits.Load() != 0 || strings.Contains(rec.Body.String(), "PRIVATE-CONTENT") {
		t.Fatalf("SSRF regression: status=%d hits=%d", rec.Code, hits.Load())
	}
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), managerTestKey) {
		t.Fatal("public homepage leaked manager key or broke")
	}
}
