package podstash

import (
	"sync"
	"testing"
	"time"
)

func TestPollHeartbeatMarkAndRead(t *testing.T) {
	h := NewPollHeartbeat()

	if _, ok := h.LastPolled("never"); ok {
		t.Error("LastPolled reported a time for an unpolled podcast")
	}

	now := time.Now().UTC()
	h.Mark("show", now)

	got, ok := h.LastPolled("show")
	if !ok {
		t.Fatal("LastPolled reported no time after Mark")
	}
	if !got.Equal(now) {
		t.Errorf("LastPolled = %v, want %v", got, now)
	}
}

func TestPollHeartbeatForget(t *testing.T) {
	h := NewPollHeartbeat()
	h.Mark("show", time.Now().UTC())
	h.Forget("show")

	if _, ok := h.LastPolled("show"); ok {
		t.Error("LastPolled reported a time after Forget")
	}
}

// The poller and HTTP handlers touch the heartbeat concurrently, so it must be
// safe under -race.
func TestPollHeartbeatConcurrentAccess(t *testing.T) {
	h := NewPollHeartbeat()
	var wg sync.WaitGroup

	for i := range 8 {
		wg.Go(func() {
			slug := string(rune('a' + i))
			for range 50 {
				h.Mark(slug, time.Now().UTC())
				h.LastPolled(slug)
				h.LastPolled("other")
			}
		})
	}
	wg.Wait()
}

// A nil heartbeat must be usable, so tests and any caller that does not set one
// get the persisted fallback rather than a panic.
func TestPollHeartbeatNilIsSafe(t *testing.T) {
	var h *PollHeartbeat

	h.Mark("show", time.Now().UTC())
	h.Forget("show")
	if _, ok := h.LastPolled("show"); ok {
		t.Error("nil heartbeat reported a poll time")
	}
}

// A delete landing while a refresh is in flight must not leave the deleted
// slug in the map: nothing reads it back, but it is never reclaimed either.
func TestPollHeartbeatForgetIsNotUndoneByLaterMark(t *testing.T) {
	h := NewPollHeartbeat()
	slug := "deleted-mid-refresh"

	// The refresh handler marks before launching its goroutine, so by the time
	// a delete calls Forget there is no later Mark to reinsert the entry.
	h.Mark(slug, time.Now().UTC())
	h.Forget(slug)

	if _, ok := h.LastPolled(slug); ok {
		t.Error("deleted slug still present in the heartbeat map")
	}
}
