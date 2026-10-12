package syncer

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Hub wakes the sync sockets of a workspace when its log grows. It carries no
// data: a woken socket reads the log itself, so a missed or duplicate wake-up
// is harmless.
type Hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan struct{}]struct{}
	// all holds the subscribers to every workspace.
	all map[chan struct{}]struct{}
}

func NewHub() *Hub {
	return &Hub{
		subs: make(map[uuid.UUID]map[chan struct{}]struct{}),
		all:  make(map[chan struct{}]struct{}),
	}
}

// SubscribeAll is Subscribe for every workspace at once.
func (h *Hub) SubscribeAll() (wake <-chan struct{}, cancel func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.all[ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		delete(h.all, ch)
		h.mu.Unlock()
	}
}

// Subscribe returns a channel that receives a value whenever the workspace's
// log may have grown. Call cancel when done.
func (h *Hub) Subscribe(workspaceID uuid.UUID) (wake <-chan struct{}, cancel func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[workspaceID] == nil {
		h.subs[workspaceID] = make(map[chan struct{}]struct{})
	}
	h.subs[workspaceID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		delete(h.subs[workspaceID], ch)
		if len(h.subs[workspaceID]) == 0 {
			delete(h.subs, workspaceID)
		}
		h.mu.Unlock()
	}
}

// Notify wakes every subscriber of a workspace.
func (h *Hub) Notify(workspaceID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[workspaceID] {
		wake(ch)
	}
	for ch := range h.all {
		wake(ch)
	}
}

// NotifyAll wakes every subscriber, for when notifications may have been missed.
func (h *Hub) NotifyAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subs := range h.subs {
		for ch := range subs {
			wake(ch)
		}
	}
	for ch := range h.all {
		wake(ch)
	}
}

func wake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default: // already has a pending wake-up
	}
}

// Listen relays Postgres notifications to the hub until ctx is done,
// reconnecting if the connection drops. Each server instance runs one.
func (h *Hub) Listen(ctx context.Context, databaseURL string, log *slog.Logger) {
	backoff := time.Second
	for ctx.Err() == nil {
		connected, err := h.listenOnce(ctx, databaseURL)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = time.Second
		}
		log.Warn("sync listener disconnected; retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (h *Hub) listenOnce(ctx context.Context, databaseURL string) (connected bool, err error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return false, err
	}
	defer conn.Close(context.WithoutCancel(ctx)) //nolint:errcheck // best effort

	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return false, err
	}
	// Anything committed while we weren't listening went unannounced.
	h.NotifyAll()

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, err
		}
		if id, err := uuid.Parse(n.Payload); err == nil {
			h.Notify(id)
		}
	}
}
