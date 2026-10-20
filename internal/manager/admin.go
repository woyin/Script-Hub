package manager

import (
	"io"
	"net/http"
	"time"
)

// adminExtra is reached only after Backend URL and Origin validation.
func (h *Handler) adminExtra(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 4 {
		return false
	}
	switch parts[3] {
	case "status":
		if len(parts) != 4 || r.Method != http.MethodGet {
			h.method(w)
			return true
		}
		rows, err := h.statuses()
		if err != nil {
			h.storeError(w, err)
		} else {
			h.json(w, 200, rows)
		}
		return true
	case "metrics":
		if len(parts) != 4 || r.Method != http.MethodGet {
			h.method(w)
		} else {
			h.json(w, 200, h.stats.snapshot())
		}
		return true
	case "backups":
		if len(parts) == 4 {
			switch r.Method {
			case http.MethodGet:
				rows, err := h.store.Backups()
				if err != nil {
					h.storeError(w, err)
				} else {
					h.json(w, 200, rows)
				}
			case http.MethodPost:
				if !h.jsonRequest(w, r) {
					return true
				}
				b, err := h.store.Snapshot()
				if err != nil {
					h.storeError(w, err)
				} else {
					h.json(w, 201, b)
				}
			default:
				h.method(w)
			}
			return true
		}
		if len(parts) != 5 || r.Method != http.MethodGet || !validBackup(parts[4]) {
			http.NotFound(w, r)
			return true
		}
		// Open under the rotation lock; rotation skips files held by active readers.
		f, release, err := h.store.openBackup(parts[4])
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		defer release()
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(h.cfg.Timeout))
		defer controller.SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+parts[4]+`"`)
		io.Copy(w, f)
		return true
	case "restore":
		if len(parts) != 4 || r.Method != http.MethodPost {
			h.method(w)
			return true
		}
		if r.Header.Get("Content-Type") != "application/gzip" {
			h.error(w, 415, "Content-Type must be application/gzip")
			return true
		}
		gated, release, ok := h.acquire(w, r)
		if !ok {
			return true
		}
		defer release()
		r = gated
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(h.cfg.Timeout))
		defer controller.SetReadDeadline(time.Time{})
		r.Body = http.MaxBytesReader(w, r.Body, backupLimit)
		count, err := h.store.Restore(r.Body)
		if err != nil {
			h.storeError(w, err)
		} else {
			rows, _ := h.allRows()
			_ = h.runtime.prune(rows)
			h.json(w, 200, map[string]int{"restored": count})
		}
		return true
	}
	return false
}
