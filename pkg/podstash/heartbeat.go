package podstash

import (
	"sync"
	"time"
)

// PollHeartbeat records when each podcast was last polled.
//
// This is deliberately in memory and never persisted. Writing a timestamp on
// every poll rewrites the podcast's meta file even when the feed has not
// changed, which on a spinning disk forces a journal commit and blocks
// spindown (issue #8). Losing the times on restart costs nothing: the poller
// runs immediately at startup, so every entry is repopulated within seconds.
type PollHeartbeat struct {
	mu    sync.RWMutex
	times map[string]time.Time
}

func NewPollHeartbeat() *PollHeartbeat {
	return &PollHeartbeat{times: make(map[string]time.Time)}
}

// Mark records that the podcast was polled at time t.
func (h *PollHeartbeat) Mark(slug string, t time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.times[slug] = t
}

// LastPolled returns when the podcast was last polled in this process, and
// whether it has been polled at all.
func (h *PollHeartbeat) LastPolled(slug string) (time.Time, bool) {
	if h == nil {
		return time.Time{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	t, ok := h.times[slug]
	return t, ok
}

// Forget drops a podcast's entry, for when the podcast is deleted.
func (h *PollHeartbeat) Forget(slug string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.times, slug)
}
