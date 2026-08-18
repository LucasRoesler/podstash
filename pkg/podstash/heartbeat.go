package podstash

import (
	"maps"
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

// RetainPodcasts drops every entry that does not correspond to one of the given
// podcasts. Callers already hold a ListPodcasts result; this saves them building
// the slug set themselves.
func (h *PollHeartbeat) RetainPodcasts(podcasts []PodcastMeta) {
	if h == nil {
		return
	}
	live := make(map[string]struct{}, len(podcasts))
	for _, p := range podcasts {
		live[p.Slug] = struct{}{}
	}
	h.Retain(live)
}

// Retain drops every entry whose slug is not in keep.
//
// Marks and deletes race by nature: the poller and the refresh handler both
// mark after releasing the per-podcast lock, so a delete landing in that gap is
// followed by a Mark that resurrects the slug. Rather than widening locks
// across those paths, the map is reconciled against the authoritative podcast
// list on read. That also stops a re-added podcast inheriting a poll time from
// its predecessor.
func (h *PollHeartbeat) Retain(keep map[string]struct{}) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	maps.DeleteFunc(h.times, func(slug string, _ time.Time) bool {
		_, ok := keep[slug]
		return !ok
	})
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
