package server

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/script-hub-org/script-hub/internal/types"
)

func TestSingleflightCanceledWaiterReleasesReference(t *testing.T) {
	flight := newSingleflight()
	entered := make(chan struct{})
	finish := make(chan struct{})
	leaderDone := make(chan struct{})
	f, err := os.CreateTemp(t.TempDir(), "body")
	if err != nil {
		t.Fatal(err)
	}
	var closed atomic.Int32
	file := types.NewFileBody(f, 0, 0, func() { closed.Add(1) })
	go func() {
		defer close(leaderDone)
		out, err := flight.do("key", func() (any, error) {
			close(entered)
			<-finish
			return conversionResult{output: types.ResponseData{Status: 200, File: file}}, nil
		})
		if err == nil {
			out.(conversionResult).Release()
		}
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := flight.doContext(ctx, "key", func() (any, error) { t.Error("waiter executed conversion"); return nil, nil })
		waiterDone <- err
	}()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	waiting := false
	for !waiting {
		select {
		case <-deadline:
			t.Fatal("waiter did not join")
		case <-ticker.C:
			flight.mu.Lock()
			waiting = flight.inflight["key"].waiters == 1
			flight.mu.Unlock()
		}
	}
	cancel()
	select {
	case err := <-waiterDone:
		if err != context.Canceled {
			t.Fatalf("waiter error %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not cancel")
	}
	close(finish)
	<-leaderDone
	if closed.Load() != 1 {
		t.Fatal("canceled waiter leaked file reference")
	}
}
