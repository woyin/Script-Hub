package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (h *Handler) compatAlias(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) > 2 && parts[2] == "download" {
		h.compat(w, r, 2)
		return true
	}
	if len(parts) < 4 || parts[2] != "api" {
		return false
	}
	switch parts[3] {
	case "subs", "sub", "file", "wholeFile", "wholeFiles":
		h.compat(w, r, 2)
		return true
	}
	return false
}
func escapedSubStoreParts(r *http.Request, offset int) ([]string, error) {
	raw := strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/")
	if len(raw) < offset {
		return nil, ErrNotFound
	}
	parts := raw[offset:]
	for i := range parts {
		v, e := url.PathUnescape(parts[i])
		if e != nil {
			return nil, ErrInvalid
		}
		parts[i] = v
	}
	return parts, nil
}
func (h *Handler) compat(w http.ResponseWriter, r *http.Request, offset int) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.Timeout)
	defer cancel()
	r = r.WithContext(ctx)
	select {
	case h.compatSlots <- struct{}{}:
		defer func() { <-h.compatSlots }()
	case <-ctx.Done():
		h.subStoreFailure(w, ctx.Err())
		return
	}
	if err := ctx.Err(); err != nil {
		h.subStoreFailure(w, err)
		return
	}
	parts, err := escapedSubStoreParts(r, offset)
	if err != nil {
		h.subStoreFailure(w, err)
		return
	}
	if len(parts) == 0 || len(parts) == 1 && parts[0] == "" {
		if r.Method == http.MethodGet {
			http.Redirect(w, r, "/manage/"+h.cfg.BackendKey+"/", http.StatusTemporaryRedirect)
		} else {
			h.subStoreMethod(w)
		}
		return
	}
	if parts[0] == "download" {
		h.subStoreDownload(w, r, parts)
		return
	}
	if parts[0] != "api" || len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	endpoint := parts[1]
	kind := "subscriptions"
	switch endpoint {
	case "subs", "sub":
	case "files", "file", "wholeFile", "wholeFiles":
		kind = "files"
	default:
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && (endpoint == "subs" || endpoint == "files" || endpoint == "wholeFiles") {
		switch r.Method {
		case http.MethodGet:
			records, err := h.store.subStoreRecords(kind)
			if err != nil {
				h.subStoreFailure(w, err)
				return
			}
			docs := make([]subStoreDocument, 0, len(records))
			for _, record := range records {
				doc := documentFor(record)
				if endpoint == "files" {
					delete(doc, "content")
				}
				docs = append(docs, doc)
			}
			h.subStoreSuccess(w, 200, docs)
		case http.MethodPost:
			if endpoint == "wholeFiles" {
				h.subStoreMethod(w)
				return
			}
			var doc subStoreDocument
			if !h.subStoreDecode(w, r, &doc) {
				return
			}
			if doc == nil {
				h.subStoreFailure(w, ErrInvalid)
				return
			}
			record, err := h.store.subStoreSave(kind, "", doc, true, "")
			if err != nil {
				h.subStoreFailure(w, err)
				return
			}
			h.subStoreSuccess(w, 201, documentFor(record))
		case http.MethodPut:
			if endpoint == "wholeFiles" {
				h.subStoreMethod(w)
				return
			}
			var docs []subStoreDocument
			if !h.subStoreDecode(w, r, &docs) {
				return
			}
			if docs == nil {
				h.subStoreFailure(w, ErrInvalid)
				return
			}
			if err := h.store.subStoreReplace(kind, docs); err != nil {
				h.subStoreFailure(w, err)
				return
			}
			h.subStoreSuccess(w, 200, nil)
		default:
			h.subStoreMethod(w)
		}
		return
	}
	if len(parts) != 3 || endpoint != "sub" && endpoint != "file" && endpoint != "wholeFile" {
		http.NotFound(w, r)
		return
	}
	name := parts[2]
	switch r.Method {
	case http.MethodGet:
		record, err := h.store.subStoreGet(kind, name)
		if err != nil {
			h.subStoreFailure(w, err)
			return
		}
		if endpoint == "file" {
			h.subStoreExport(w, r, record, "text")
			return
		}
		doc := documentFor(record)
		w.Header().Set("ETag", `"`+revision(record)+`"`)
		if r.URL.Query().Get("raw") != "" {
			kindName := "subscription"
			if kind == "files" {
				kindName = "file"
			}
			filename := url.PathEscape("sub-store_" + kindName + "_" + record.Spec.Name + ".json")
			w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
			w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
			h.json(w, 200, doc)
		} else {
			h.subStoreSuccess(w, 200, doc)
		}
	case http.MethodPatch:
		if endpoint == "wholeFile" {
			h.subStoreMethod(w)
			return
		}
		var patch subStoreDocument
		if !h.subStoreDecode(w, r, &patch) {
			return
		}
		if patch == nil {
			h.subStoreFailure(w, ErrInvalid)
			return
		}
		record, err := h.store.subStoreSave(kind, name, patch, false, strings.Trim(r.Header.Get("If-Match"), `"`))
		if err != nil {
			h.subStoreFailure(w, err)
			return
		}
		h.subStoreSuccess(w, 200, documentFor(record))
	case http.MethodDelete:
		if endpoint == "wholeFile" {
			h.subStoreMethod(w)
			return
		}
		if mode := r.URL.Query().Get("mode"); mode != "" && mode != "permanent" {
			h.subStoreFailure(w, errSubStoreUnsupported)
			return
		}
		if err := h.store.subStoreDelete(kind, name, strings.Trim(r.Header.Get("If-Match"), `"`)); err != nil {
			h.subStoreFailure(w, err)
			return
		}
		h.subStoreSuccess(w, 200, nil)
	default:
		h.subStoreMethod(w)
	}
}
func (h *Handler) subStoreDownload(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodGet {
		h.subStoreMethod(w)
		return
	}
	if len(parts) < 2 || len(parts) > 3 {
		http.NotFound(w, r)
		return
	}
	record, err := h.store.subStoreGet("subscriptions", parts[1])
	if err != nil {
		h.subStoreFailure(w, err)
		return
	}
	target := r.URL.Query().Get("platform")
	if target == "" {
		target = r.URL.Query().Get("target")
	}
	if len(parts) == 3 {
		target = parts[2]
	}
	if target == "" {
		ua := strings.ToLower(r.UserAgent())
		switch {
		case strings.Contains(ua, "shadowrocket"):
			target = "shadowrocket"
		case strings.Contains(ua, "egern"):
			target = "egern"
		case strings.Contains(ua, "loon"):
			target = "loon"
		default:
			target = "clash"
		}
	}
	switch strings.ToLower(target) {
	case "clash", "clashmeta", "mihomo", "stash":
		target = "clash"
	case "shadowrocket", "v2ray":
		target = "shadowrocket"
	case "loon":
		target = "loon"
	case "egern":
		target = "egern"
	case "uri":
		target = "uri"
	default:
		h.subStoreFailure(w, errSubStoreUnsupported)
		return
	}
	h.subStoreExport(w, r, record, target)
}
func (h *Handler) subStoreExport(w http.ResponseWriter, r *http.Request, record Record, target string) {
	for _, key := range []string{"url", "content", "$options", "source", "sourceType", "sourceName", "process"} {
		if r.URL.Query().Get(key) != "" {
			h.subStoreFailure(w, errSubStoreUnsupported)
			return
		}
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	copy.URL = &u
	q := u.Query()
	q.Set("target", target)
	if strings.EqualFold(q.Get("noCache"), "true") || q.Get("noCache") == "1" {
		q.Set("refresh", "true")
	}
	copy.URL.RawQuery = q.Encode()
	wrapped := &subStoreExportWriter{ResponseWriter: w}
	h.handleExport(wrapped, copy, record.Kind, record.ID, "", q.Get("refresh") == "true")
	if wrapped.status >= 400 {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(wrapped.body.Bytes(), &body)
		if body.Error == "" {
			body.Error = "resource export failed"
		}
		h.subStoreError(w, wrapped.status, "EXPORT_FAILED", "InternalServerError", body.Error)
	}
}

// Preserve streaming, bounded readers and cold conversion admission. Only the
// small error body is intercepted to supply the original API's error envelope.
type subStoreExportWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *subStoreExportWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *subStoreExportWriter) WriteHeader(status int) {
	w.status = status
	if status < 400 {
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *subStoreExportWriter) Write(b []byte) (int, error) {
	if w.status >= 400 {
		if w.body.Len() < 65536 {
			_, _ = w.body.Write(b[:min(len(b), 65536-w.body.Len())])
		}
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}
func (h *Handler) subStoreSuccess(w http.ResponseWriter, status int, data any) {
	v := map[string]any{"status": "success"}
	if data != nil {
		v["data"] = data
	}
	h.json(w, status, v)
}
func (h *Handler) subStoreError(w http.ResponseWriter, status int, code, typ, message string) {
	h.json(w, status, map[string]any{"status": "failed", "error": map[string]string{"code": code, "type": typ, "message": message}})
}
func (h *Handler) subStoreMethod(w http.ResponseWriter) {
	h.subStoreError(w, 405, "METHOD_NOT_ALLOWED", "RequestInvalidError", "method not allowed")
}
func (h *Handler) subStoreFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.subStoreError(w, 404, "RESOURCE_NOT_FOUND", "ResourceNotFoundError", "resource does not exist")
	case errors.Is(err, errSubStoreDuplicate):
		h.subStoreError(w, 400, "DUPLICATE_KEY", "RequestInvalidError", "resource name already exists")
	case errors.Is(err, errSubStoreAmbiguous), errors.Is(err, ErrConflict):
		h.subStoreError(w, 409, "RESOURCE_CONFLICT", "RequestInvalidError", "resource changed or name is ambiguous")
	case errors.Is(err, errSubStoreUnsupported):
		h.subStoreError(w, 422, "UNSUPPORTED_FEATURE", "RequestInvalidError", err.Error())
	case errors.Is(err, ErrInvalid):
		h.subStoreError(w, 400, "INVALID_REQUEST", "RequestInvalidError", err.Error())
	case errors.Is(err, ErrLimit):
		h.subStoreError(w, 413, "RESOURCE_LIMIT", "RequestInvalidError", "resource limit exceeded")
	case errors.Is(err, context.DeadlineExceeded):
		h.subStoreError(w, 504, "TIMEOUT", "InternalServerError", "request timed out")
	case errors.Is(err, context.Canceled):
		h.subStoreError(w, 408, "CANCELLED", "InternalServerError", "request cancelled")
	default:
		h.subStoreError(w, 500, "INTERNAL_ERROR", "InternalServerError", "resource operation failed")
	}
}
func (h *Handler) subStoreDecode(w http.ResponseWriter, r *http.Request, out any) bool {
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		h.subStoreError(w, 415, "INVALID_CONTENT_TYPE", "RequestInvalidError", "Content-Type must be application/json")
		return false
	}
	controller := http.NewResponseController(w)
	if deadline, ok := r.Context().Deadline(); ok {
		_ = controller.SetReadDeadline(deadline)
	}
	defer controller.SetReadDeadline(time.Time{})
	r.Body = http.MaxBytesReader(w, r.Body, MaxDocumentBytes)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(out); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			h.subStoreFailure(w, ErrLimit)
		} else {
			h.subStoreFailure(w, ErrInvalid)
		}
		return false
	}
	if err := r.Context().Err(); err != nil {
		h.subStoreFailure(w, err)
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		h.subStoreFailure(w, ErrInvalid)
		return false
	}
	return true
}
