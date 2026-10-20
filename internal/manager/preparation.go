package manager

import (
	"context"

	"github.com/script-hub-org/script-hub/internal/subscription"
)

type preparation struct {
	done  chan struct{}
	refs  int
	nodes []subscription.Node
	err   error
}

// A lease exists only while admitted conversions overlap. Queued requests do not
// retain nodes; the last active conversion clears both the slice and its map entry.
func (h *Handler) prepare(ctx context.Context, r Record, force bool) ([]subscription.Node, func(), error) {
	key := r.ID + "/" + revision(r)
	if force {
		key += "/force"
	}
	h.prepMu.Lock()
	p := h.preparations[key]
	owner := p == nil
	if owner {
		p = &preparation{done: make(chan struct{})}
		h.preparations[key] = p
	}
	p.refs++
	h.prepMu.Unlock()
	release := func() {
		h.prepMu.Lock()
		defer h.prepMu.Unlock()
		p.refs--
		if p.refs == 0 {
			delete(h.preparations, key)
			p.nodes = nil
		}
	}
	if owner {
		p.nodes, p.err = h.readNodes(ctx, r)
		close(p.done)
	} else {
		select {
		case <-p.done:
		case <-ctx.Done():
			release()
			return nil, func() {}, ctx.Err()
		}
	}
	if p.err != nil {
		release()
		return nil, func() {}, p.err
	}
	return p.nodes, release, nil
}
