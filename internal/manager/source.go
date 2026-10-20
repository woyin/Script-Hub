package manager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/script-hub-org/script-hub/internal/cache"
	"github.com/script-hub-org/script-hub/internal/types"
)

type ConditionalFetchFunc func(context.Context, string, map[string]string, io.Writer) (int, http.Header, error)
type sourceFlight struct {
	done chan struct{}
	err  error
}
type refreshSourceKey struct{}

func closeBody(rd types.ResponseData) {
	if rd.File != nil {
		rd.File.Close()
	}
}
func copyBody(dst io.Writer, rd types.ResponseData) error {
	var r io.Reader = strings.NewReader(rd.Body)
	if rd.File != nil {
		r = rd.File.Reader()
	}
	_, err := io.Copy(dst, r)
	return err
}
func sourceKey(source string, headers map[string]string) string {
	canonical := map[string]string{}
	for k, v := range headers {
		canonical[http.CanonicalHeaderKey(k)] = v
	}
	b, _ := json.Marshal(struct {
		URL     string
		Headers map[string]string
	}{source, canonical})
	return "manager-source:v1:" + digest(string(b))
}

// Source bytes live only in the common disk cache. Locks merge requests across targets.
func (h *Handler) fetch(ctx context.Context, source string, headers map[string]string, dst io.Writer) (err error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() { h.stats.observe("fetch", time.Since(started)) }()
	key := sourceKey(source, headers)
	force, _ := ctx.Value(refreshSourceKey{}).(time.Time)
	if rd, ok := h.cfg.Cache.Get(key); ok {
		checked, _ := time.Parse(time.RFC3339Nano, rd.Headers["Validated-At"])
		if force.IsZero() || !checked.Before(force) {
			h.stats.sourceHits.Add(1)
			defer closeBody(rd)
			return copyBody(&budgetWriter{dst: dst, left: h.cfg.SourceMaxBytes}, rd)
		}
		closeBody(rd)
	}
	h.sourceMu.Lock()
	if until := h.sourceFailures[key]; force.IsZero() && time.Now().Before(until) {
		h.sourceMu.Unlock()
		h.stats.sourceCooldownHits.Add(1)
		return errSourceCoolingDown
	}
	if h.cfg.Cache == nil {
		h.sourceMu.Unlock()
		err = h.fetchRaw(ctx, source, headers, dst)
		h.sourceMu.Lock()
		h.recordSourceResult(key, err)
		h.sourceMu.Unlock()
		return err
	}
	if done := h.sourceFlights[key]; done != nil {
		h.sourceMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done.done:
		}
		if done.err != nil {
			return done.err
		}
		if rd, ok := h.cfg.Cache.Get(key); ok {
			defer closeBody(rd)
			h.stats.sourceHits.Add(1)
			return copyBody(&budgetWriter{dst: dst, left: h.cfg.SourceMaxBytes}, rd)
		}
		return errors.New("source refresh failed")
	}
	done := &sourceFlight{done: make(chan struct{})}
	h.sourceFlights[key] = done
	h.sourceMu.Unlock()
	defer func() {
		h.sourceMu.Lock()
		delete(h.sourceFlights, key)
		h.recordSourceResult(key, err)
		done.err = err
		close(done.done)
		h.sourceMu.Unlock()
	}()
	// A completed flight may have published between the initial lookup and locking.
	if rd, ok := h.cfg.Cache.Get(key); ok {
		checked, _ := time.Parse(time.RFC3339Nano, rd.Headers["Validated-At"])
		if force.IsZero() || !checked.Before(force) {
			defer closeBody(rd)
			return copyBody(&budgetWriter{dst: dst, left: h.cfg.SourceMaxBytes}, rd)
		}
		closeBody(rd)
	}
	old, hasOld := h.cfg.Cache.GetRetained(key)
	if hasOld {
		defer closeBody(old)
	}
	requestHeaders := map[string]string{}
	for k, v := range headers {
		if !strings.EqualFold(k, "If-None-Match") && !strings.EqualFold(k, "If-Modified-Since") {
			requestHeaders[k] = v
		}
	}
	if hasOld {
		if v := old.Headers["ETag"]; v != "" {
			requestHeaders["If-None-Match"] = v
		}
		if v := old.Headers["Last-Modified"]; v != "" {
			requestHeaders["If-Modified-Since"] = v
		}
	}
	ws, err := h.cfg.Cache.Workspace(key)
	if err != nil {
		return err
	}
	defer ws.Close()
	f, err := ws.Create()
	if err != nil {
		return err
	}
	defer f.Close()
	h.stats.sourceMisses.Add(1)
	status := http.StatusOK
	responseHeaders := http.Header{}
	if h.cfg.FetchConditional != nil {
		status, responseHeaders, err = h.cfg.FetchConditional(ctx, source, requestHeaders, &budgetWriter{dst: f, left: h.cfg.SourceMaxBytes})
	} else {
		err = h.fetchRaw(ctx, source, requestHeaders, f)
	}
	if err != nil {
		h.stats.fetchErrors.Add(1)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, cache.ErrCapacity) || errors.Is(err, cache.ErrUnavailable) {
			return err
		}
		return errors.New("remote source failed or exceeded size limit")
	}
	metadata := map[string]string{"Validated-At": time.Now().UTC().Format(time.RFC3339Nano)}
	var rd types.ResponseData
	switch status {
	case http.StatusNotModified:
		if !hasOld {
			return errors.New("source returned 304 without cached content")
		}
		h.stats.notModified.Add(1)
		rd = old
		for _, k := range []string{"ETag", "Last-Modified"} {
			metadata[k] = old.Headers[k]
		}
	case http.StatusOK:
		rd = types.ResponseData{Status: 200, File: types.NewFileBody(f.File, 0, f.Size(), nil)}
	default:
		h.stats.fetchErrors.Add(1)
		return errors.New("source HTTP failure: " + strconv.Itoa(status))
	}
	for _, k := range []string{"ETag", "Last-Modified"} {
		if v := responseHeaders.Get(k); len(v) <= 4096 && v != "" {
			metadata[k] = v
		}
	}
	rd.Headers = metadata
	if err := h.cfg.Cache.SetLifetime(key, rd, h.cfg.CacheTTL, h.cfg.StaleTTL); err != nil {
		return err
	}
	fresh, ok := h.cfg.Cache.Get(key)
	if !ok {
		return cache.ErrUnavailable
	}
	defer closeBody(fresh)
	return copyBody(&budgetWriter{dst: dst, left: h.cfg.SourceMaxBytes}, fresh)
}

var errSourceCoolingDown = errors.New("remote source is cooling down after a failure")

// Caller holds sourceMu. Only upstream failures cool down; local disk and cancellation do not.
func (h *Handler) recordSourceResult(key string, err error) {
	if err == nil {
		delete(h.sourceFailures, key)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, cache.ErrCapacity) || errors.Is(err, cache.ErrUnavailable) {
		return
	}
	now := time.Now()
	for k, until := range h.sourceFailures {
		if !now.Before(until) {
			delete(h.sourceFailures, k)
		}
	}
	if len(h.sourceFailures) >= 4096 {
		var oldest string
		var at time.Time
		for k, t := range h.sourceFailures {
			if oldest == "" || t.Before(at) {
				oldest = k
				at = t
			}
		}
		delete(h.sourceFailures, oldest)
	}
	h.sourceFailures[key] = now.Add(h.cfg.SourceRetryCooldown)
}
