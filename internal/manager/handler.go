package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/script-hub-org/script-hub/internal/cache"
	"github.com/script-hub-org/script-hub/internal/subscription"
	"github.com/script-hub-org/script-hub/internal/types"
)

type FetchFunc func(context.Context, string, map[string]string, io.Writer) error
type Config struct {
	BackendKey     string
	DataDir        string
	CacheDir       string
	MaxBytes       int64
	SourceMaxBytes int64
	Concurrency    int
	Timeout        time.Duration
	Version        string
	Cache          *cache.Cache
	Fetch          FetchFunc
}
type flight struct {
	done chan struct{}
	err  error
	body types.ResponseData
}
type Handler struct {
	cfg     Config
	store   *Store
	key     [32]byte
	slots   chan struct{}
	mu      sync.Mutex
	flights map[string]*flight
}

func New(cfg Config) (*Handler, error) {
	if len(cfg.BackendKey) < 32 || len(cfg.BackendKey) > 128 || strings.Trim(cfg.BackendKey, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return nil, errors.New("MANAGER_BACKEND_KEY must contain 32–128 random URL-safe characters")
	}
	data, err := canonicalPath(cfg.DataDir)
	if err != nil || cfg.DataDir == "" {
		return nil, errors.New("invalid management data directory")
	}
	if cfg.CacheDir != "" {
		c, err := canonicalPath(cfg.CacheDir)
		if err != nil {
			return nil, err
		}
		if data == c || strings.HasPrefix(data, c+string(filepath.Separator)) || strings.HasPrefix(c, data+string(filepath.Separator)) {
			return nil, errors.New("management data directory and cache directory must not overlap")
		}
	}
	if cfg.SourceMaxBytes <= 0 {
		cfg.SourceMaxBytes = MaxDocumentBytes
	}
	if cfg.SourceMaxBytes > MaxDocumentBytes {
		return nil, errors.New("management sources cannot exceed 8 MiB")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	if cfg.Concurrency > 35 {
		return nil, errors.New("management concurrency cannot exceed 35")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	store, err := NewStore(data, cfg.MaxBytes)
	if err != nil {
		return nil, err
	}
	return &Handler{cfg: cfg, store: store, key: sha256.Sum256([]byte(cfg.BackendKey)), slots: make(chan struct{}, cfg.Concurrency), flights: map[string]*flight{}}, nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) > 0 && parts[0] == "share" {
		h.share(w, r, parts)
		return
	}
	if len(parts) < 2 || parts[0] != "manage" {
		http.NotFound(w, r)
		return
	}
	key := sha256.Sum256([]byte(parts[1]))
	if subtle.ConstantTimeCompare(key[:], h.key[:]) != 1 {
		http.NotFound(w, r)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		if err != nil || u.Host != r.Host || u.Scheme != scheme || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			h.error(w, http.StatusForbidden, "cross-origin management request rejected")
			return
		}
	}
	if len(parts) == 2 {
		if r.Method != http.MethodGet {
			h.method(w)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, managementPage)
		return
	}
	if len(parts) < 4 || parts[2] != "api" {
		http.NotFound(w, r)
		return
	}
	if parts[3] == "capabilities" && len(parts) == 4 && r.Method == http.MethodGet {
		h.json(w, http.StatusOK, subscription.Capabilities())
		return
	}
	kind := parts[3]
	if kind != "subscriptions" && kind != "files" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 4 {
		switch r.Method {
		case http.MethodGet:
			rows, err := h.store.List(kind)
			if err != nil {
				h.storeError(w, err)
				return
			}
			h.json(w, http.StatusOK, rows)
		case http.MethodPost:
			var spec Spec
			if !h.decode(w, r, &spec) {
				return
			}
			record, err := h.store.Save(kind, "", spec)
			if err != nil {
				h.storeError(w, err)
				return
			}
			h.json(w, http.StatusCreated, publicRecord(record))
		default:
			h.method(w)
		}
		return
	}
	if len(parts) > 6 {
		http.NotFound(w, r)
		return
	}
	id := parts[4]
	if len(parts) == 6 {
		switch parts[5] {
		case "export":
			if r.Method != http.MethodGet {
				h.method(w)
				return
			}
			gated, release, ok := h.acquire(w, r)
			if !ok {
				return
			}
			defer release()
			r = gated
			record, err := h.store.Get(kind, id)
			if err != nil {
				h.storeError(w, err)
				return
			}
			h.serveExport(w, r, record, r.URL.Query().Get("refresh") == "true")
		case "share":
			if r.Method != http.MethodPost && r.Method != http.MethodDelete {
				h.method(w)
				return
			}
			if !h.jsonRequest(w, r) {
				return
			}
			token, err := h.store.Share(kind, id, r.Method == http.MethodDelete)
			if err != nil {
				h.storeError(w, err)
				return
			}
			if token == "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			h.json(w, http.StatusOK, map[string]string{"path": "/share/" + kind + "/" + id + "/" + token})
		default:
			http.NotFound(w, r)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		record, err := h.store.Get(kind, id)
		if err != nil {
			h.storeError(w, err)
			return
		}
		h.json(w, http.StatusOK, publicRecord(record))
	case http.MethodPut:
		var spec Spec
		if !h.decode(w, r, &spec) {
			return
		}
		record, err := h.store.Save(kind, id, spec)
		if err != nil {
			h.storeError(w, err)
			return
		}
		h.json(w, http.StatusOK, publicRecord(record))
	case http.MethodDelete:
		if !h.jsonRequest(w, r) {
			return
		}
		if err := h.store.Delete(kind, id); err != nil {
			h.storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		h.method(w)
	}
}
func publicRecord(r Record) any {
	return struct {
		ID        string    `json:"id"`
		Kind      string    `json:"kind"`
		Spec      Spec      `json:"spec"`
		UpdatedAt time.Time `json:"updatedAt"`
		Shared    bool      `json:"shared"`
	}{r.ID, r.Kind, r.Spec, r.UpdatedAt, r.ShareDigest != ""}
}
func (h *Handler) share(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 4 || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("refresh") != "" {
		h.error(w, http.StatusForbidden, "share links cannot force refresh")
		return
	}
	gated, release, ok := h.acquire(w, r)
	if !ok {
		return
	}
	defer release()
	r = gated
	record, err := h.store.Get(parts[1], parts[2])
	if err != nil || !CheckShare(record, parts[3]) {
		http.NotFound(w, r)
		return
	}
	h.serveExport(w, r, record, false)
}
func (h *Handler) serveExport(w http.ResponseWriter, r *http.Request, record Record, force bool) {
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "clash"
	}
	if record.Kind == "files" {
		target = "text"
	} else {
		switch target {
		case "clash", "shadowrocket", "uri", "loon", "egern":
		default:
			h.error(w, http.StatusBadRequest, "unknown subscription target")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.Timeout)
	defer cancel()
	rd, state, err := h.export(ctx, record, target, force)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			h.error(w, http.StatusGatewayTimeout, "export timed out")
		case errors.Is(err, context.Canceled):
			h.error(w, http.StatusRequestTimeout, "export cancelled")
		case errors.Is(err, cache.ErrCapacity), errors.Is(err, cache.ErrUnavailable):
			h.error(w, http.StatusServiceUnavailable, "export disk space unavailable")
		default:
			h.error(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}
	// A slow client must not hold a private export slot indefinitely.
	controller := http.NewResponseController(w)
	if deadline, ok := ctx.Deadline(); ok {
		_ = controller.SetWriteDeadline(deadline)
		defer controller.SetWriteDeadline(time.Time{})
	}
	if rd.File != nil {
		defer rd.File.Close()
	}
	if ct := rd.Headers["Content-Type"]; ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("X-ScriptHub-Cache", state)
	// Keep all exports as data; file content is never served as HTML/JavaScript.
	if rd.File != nil {
		io.Copy(w, rd.File.Reader())
	} else {
		io.WriteString(w, rd.Body)
	}
}
func (h *Handler) export(ctx context.Context, r Record, target string, force bool) (types.ResponseData, string, error) {
	revision, _ := json.Marshal(struct {
		Version   string
		ID        string
		Spec      Spec
		UpdatedAt time.Time
		Target    string
	}{h.cfg.Version, r.ID, r.Spec, r.UpdatedAt, target})
	key := "managed:" + digest(string(revision))
	if !force {
		if rd, ok := h.cfg.Cache.Get(key); ok {
			return rd, "HIT", nil
		}
	}
	h.mu.Lock()
	if existing := h.flights[key]; existing != nil {
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return types.ResponseData{}, "", ctx.Err()
		case <-existing.done:
		}
		if existing.err != nil {
			return types.ResponseData{}, "", existing.err
		}
		rd, err := h.result(key, existing)
		state := "MISS"
		if force {
			state = "REFRESH"
		}
		if h.cfg.Cache == nil {
			state = "BYPASS"
		}
		return rd, state, err
	}
	f := &flight{done: make(chan struct{})}
	h.flights[key] = f
	h.mu.Unlock()
	f.body, f.err = h.produce(ctx, r, key, target)
	h.mu.Lock()
	delete(h.flights, key)
	close(f.done)
	h.mu.Unlock()
	if f.err != nil {
		return types.ResponseData{}, "", f.err
	}
	rd, err := h.result(key, f)
	state := "MISS"
	if force {
		state = "REFRESH"
	}
	if h.cfg.Cache == nil {
		state = "BYPASS"
	}
	return rd, state, err
}
func (h *Handler) result(key string, f *flight) (types.ResponseData, error) {
	if h.cfg.Cache == nil {
		return f.body, nil
	}
	rd, ok := h.cfg.Cache.Get(key)
	if !ok {
		return types.ResponseData{}, cache.ErrUnavailable
	}
	return rd, nil
}
func (h *Handler) produce(ctx context.Context, r Record, key, target string) (types.ResponseData, error) {
	if err := ctx.Err(); err != nil {
		return types.ResponseData{}, err
	}

	rd := types.ResponseData{Status: http.StatusOK, Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"}}
	if r.Kind == "files" {
		if r.Spec.Source == "local" {
			rd.Body = r.Spec.Content
		} else if h.cfg.Cache != nil {
			ws, err := h.cfg.Cache.Workspace(key)
			if err != nil {
				return types.ResponseData{}, err
			}
			f, err := ws.Create()
			if err != nil {
				ws.Close()
				return types.ResponseData{}, err
			}
			if err := h.fetch(ctx, r.Spec.URLs[0], r.Spec.Headers, f); err != nil {
				f.Close()
				ws.Close()
				return types.ResponseData{}, err
			}
			info, err := f.Stat()
			if err != nil {
				f.Close()
				ws.Close()
				return types.ResponseData{}, err
			}
			rd.File = types.NewFileBody(f.File, 0, info.Size(), ws.Close)
		} else {
			var b bytes.Buffer
			if err := h.fetch(ctx, r.Spec.URLs[0], r.Spec.Headers, &b); err != nil {
				return types.ResponseData{}, err
			}
			rd.Body = b.String()
		}
	} else {
		var all []subscription.Node
		if r.Spec.Source == "local" {
			nodes, err := subscription.Parse([]byte(r.Spec.Content))
			if err != nil {
				return types.ResponseData{}, err
			}
			all = nodes
		} else {
			var total int64
			for i, source := range r.Spec.URLs {
				var b bytes.Buffer
				limited := &budgetWriter{dst: &b, left: h.cfg.SourceMaxBytes - total}
				if err := h.fetch(ctx, source, r.Spec.Headers, limited); err != nil {
					return types.ResponseData{}, fmt.Errorf("source %d: %w", i+1, err)
				}
				total += int64(b.Len())
				nodes, err := subscription.Parse(b.Bytes())
				if err != nil {
					return types.ResponseData{}, fmt.Errorf("source %d: %w", i+1, err)
				}
				all = append(all, nodes...)
				if len(all) > subscription.MaxNodes {
					return types.ResponseData{}, errors.New("too many combined nodes")
				}
			}
		}
		all, err := subscription.Process(all, r.Spec.Processing)
		if err != nil {
			return types.ResponseData{}, err
		}
		if err := ctx.Err(); err != nil {
			return types.ResponseData{}, err
		}
		output, err := subscription.Export(all, target)
		if err != nil {
			return types.ResponseData{}, err
		}
		rd.Body = string(output.Body)
		rd.Headers["Content-Type"] = output.ContentType
	}
	if rd.File != nil {
		defer rd.File.Close()
	}
	if err := ctx.Err(); err != nil {
		return types.ResponseData{}, err
	}
	if h.cfg.Cache != nil {
		if err := h.cfg.Cache.Set(key, rd); err != nil {
			return types.ResponseData{}, err
		}
		return types.ResponseData{}, nil
	}
	return rd, nil
}

type budgetWriter struct {
	dst  io.Writer
	left int64
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		return 0, errors.New("source data limit exceeded")
	}
	n, err := w.dst.Write(p)
	w.left -= int64(n)
	return n, err
}
func (h *Handler) fetch(ctx context.Context, source string, headers map[string]string, dst io.Writer) error {
	if h.cfg.Fetch == nil {
		return errors.New("remote source fetching is unavailable")
	}
	limit := &budgetWriter{dst: dst, left: h.cfg.SourceMaxBytes}
	if err := h.cfg.Fetch(ctx, source, headers, limit); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, cache.ErrCapacity) || errors.Is(err, cache.ErrUnavailable) {
			return err
		}
		return errors.New("remote source failed or exceeded size limit")
	}
	return nil
}
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if !h.jsonRequest(w, r) {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxDocumentBytes)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		h.error(w, http.StatusBadRequest, "invalid JSON request or body exceeds 8 MiB")
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		h.error(w, http.StatusBadRequest, "exactly one JSON document is required")
		return false
	}
	return true
}
func (h *Handler) jsonRequest(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		h.error(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}
func (h *Handler) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func (h *Handler) error(w http.ResponseWriter, status int, message string) {
	h.json(w, status, map[string]string{"error": message})
}
func (h *Handler) method(w http.ResponseWriter) {
	h.error(w, http.StatusMethodNotAllowed, "method not allowed")
}
func (h *Handler) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, nil)
	case errors.Is(err, ErrInvalid):
		h.error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrLimit):
		h.error(w, http.StatusInsufficientStorage, "management data limit exceeded")
	default:
		h.error(w, http.StatusInternalServerError, "management storage operation failed")
	}
}

// Resolve existing parent symlinks before checking directory separation.
func canonicalPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(abs)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", err
		}
		tail = append(tail, filepath.Base(abs))
		abs = parent
	}
}

// Gate before loading records and constructing cache keys: local subscription bodies
// must not be duplicated by an unbounded number of concurrent share requests.
func (h *Handler) acquire(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.Timeout)
	if ctx.Err() != nil {
		cancel()
		h.error(w, http.StatusRequestTimeout, "export cancelled")
		return r, func() {}, false
	}
	select {
	case h.slots <- struct{}{}:
		return r.WithContext(ctx), func() { <-h.slots; cancel() }, true
	case <-ctx.Done():
		cancel()
		status := http.StatusGatewayTimeout
		if errors.Is(ctx.Err(), context.Canceled) {
			status = http.StatusRequestTimeout
		}
		h.error(w, status, "export queue timed out or cancelled")
		return r, func() {}, false
	}
}
