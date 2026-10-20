package cache

import (
	"github.com/script-hub-org/script-hub/internal/types"
	"path/filepath"
	"testing"
	"time"
)

func TestRetainedExpirationSurvivesCleanupAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	c, err := New(dir, 60, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetLifetime("key", types.ResponseData{Status: 200, Body: "last good"}, 10*time.Millisecond, time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if rd, ok := c.Get("key"); ok {
		rd.File.Close()
		t.Fatal("expired data is fresh")
	}
	if err = c.Cleanup(); err != nil {
		t.Fatal(err)
	}
	c, err = New(dir, 60, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	rd, ok := c.GetRetained("key")
	if !ok {
		t.Fatal("retained cache lost on restart")
	}
	rd.File.Close()
	if rd, ok = c.Get("key"); ok {
		rd.File.Close()
		t.Fatal("restart made stale data fresh")
	}
}
