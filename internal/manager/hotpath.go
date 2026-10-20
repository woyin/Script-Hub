package manager

import (
	"context"
	"errors"
	"net/http"

	"github.com/script-hub-org/script-hub/internal/cache"
)

func (h *Handler) signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *Handler) readPermit(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return func() {}, err
	}
	select {
	case h.readSlots <- struct{}{}:
		return func() { <-h.readSlots }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}
func (h *Handler) exportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		h.error(w, 504, "export timed out")
	case errors.Is(err, context.Canceled):
		h.error(w, 408, "export cancelled")
	case errors.Is(err, cache.ErrCapacity), errors.Is(err, cache.ErrUnavailable):
		h.error(w, 503, "export disk space unavailable")
	default:
		h.error(w, 422, err.Error())
	}
}
func exportTarget(kind, requested string) (string, bool) {
	if kind == "files" {
		return "text", true
	}
	if requested == "" {
		return "clash", true
	}
	switch requested {
	case "clash", "shadowrocket", "loon", "egern", "uri":
		return requested, true
	}
	return "", false
}
func (h *Handler) authorizeMeta(w http.ResponseWriter, kind, id, token string) (resourceMeta, bool) {
	m, err := h.store.Meta(kind, id)
	if err != nil || token != "" && !CheckShare(m.record(), token) {
		http.NotFound(w, nil)
		return resourceMeta{}, false
	}
	return m, true
}

// Warm responses use only small metadata and their own bounded stream permits.
// Cold work retains its conversion permit until it can hand the file to a reader.
func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request, kind, id, token string, force bool) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.Timeout)
	defer cancel()
	r = r.WithContext(ctx)
	if _, ok := h.authorizeMeta(w, kind, id, token); !ok {
		return
	}
	target, ok := exportTarget(kind, r.URL.Query().Get("target"))
	if !ok {
		h.error(w, 400, "unknown subscription target")
		return
	}
	if !force {
		release, err := h.readPermit(ctx)
		if err != nil {
			h.exportError(w, err)
			return
		}
		meta, ok := h.authorizeMeta(w, kind, id, token)
		if !ok {
			release()
			return
		}
		if rd, hit := h.cfg.Cache.Get(outputKey(meta.record(), target)); hit {
			defer release()
			h.stats.outputHits.Add(1)
			h.writeExport(w, r, rd, "HIT")
			return
		}
		release()
	}
	gated, release, ok := h.acquire(w, r)
	if !ok {
		return
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	record, err := h.store.Get(kind, id)
	if err != nil || token != "" && !CheckShare(record, token) {
		http.NotFound(w, r)
		return
	}
	rd, state, err := h.export(gated.Context(), record, target, force)
	if err != nil {
		h.exportError(w, err)
		return
	}
	releaseRead, err := h.readPermit(ctx)
	if err != nil {
		closeBody(rd)
		h.exportError(w, err)
		return
	}
	defer releaseRead()
	release()
	release = nil
	// Recheck shares after waiting for a stream permit; revocation and restore remain effective.
	meta, ok := h.authorizeMeta(w, kind, id, token)
	if !ok {
		closeBody(rd)
		return
	}
	if revision(meta.record()) != revision(record) {
		closeBody(rd)
		h.error(w, 409, "resource changed during export")
		return
	}
	h.writeExport(w, r, rd, state)
}
