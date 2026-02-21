package block

import (
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type slotEntry struct {
	session *fetchSession
	done    chan struct{} // closed when session releases slots
}

// regionLock tracks active fetch sessions across MemoryChunkSize-aligned slots.
// It provides both session deduplication (join an existing session whose range
// covers the request) and overlap prevention (wait for occupied slots to clear
// before claiming a multi-slot range).
//
// Uncompressed fetches occupy 1 slot; compressed frames may span 1–4 slots.
type regionLock struct {
	mu    sync.Mutex
	slots []*slotEntry // nil = free, non-nil = active session
}

func newRegionLock(fileSize int64) *regionLock {
	n := (fileSize + storage.MemoryChunkSize - 1) / storage.MemoryChunkSize

	return &regionLock{
		slots: make([]*slotEntry, n),
	}
}

// slotRange returns the [start, end) slot indices for a byte range.
func slotRange(off, length int64) (start, end int64) {
	start = off / storage.MemoryChunkSize
	end = (off + length + storage.MemoryChunkSize - 1) / storage.MemoryChunkSize

	return start, end
}

// getOrCreate returns an existing session covering the requested range, or
// creates a new one using createFn and claims the slots.
//
// When all slots in [start, end) are occupied by the same session whose range
// is a superset of [chunkOff, chunkOff+chunkLen), the caller joins it.
// When any slot has a different or insufficient session, the caller waits for
// it to clear and retries. When all slots are free, a new session is created.
//
// Returns (session, isNew). When isNew is true, the caller must launch the
// fetch goroutine and call release() when done.
func (r *regionLock) getOrCreate(chunkOff, chunkLen int64, createFn func() *fetchSession) (*fetchSession, bool) {
	start, end := slotRange(chunkOff, chunkLen)

	for {
		r.mu.Lock()

		var (
			match  *fetchSession
			waitOn chan struct{}
		)

		allMatch := true

		for i := start; i < end; i++ {
			entry := r.slots[i]
			if entry == nil {
				allMatch = false

				continue
			}

			if match == nil {
				match = entry.session
				waitOn = entry.done
			} else if entry.session != match {
				allMatch = false
			}
		}

		// All slots occupied by the same session — check if it covers our range.
		if match != nil && allMatch {
			if match.chunkOff <= chunkOff && match.chunkOff+match.chunkLen >= chunkOff+chunkLen {
				r.mu.Unlock()

				return match, false
			}

			// Session doesn't cover our range (e.g., smaller frame in same slot).
			r.mu.Unlock()
			<-waitOn

			continue
		}

		// Some slots occupied by different sessions — wait for one to clear.
		if match != nil {
			r.mu.Unlock()
			<-waitOn

			continue
		}

		// All slots free — create and claim.
		s := createFn()
		entry := &slotEntry{session: s, done: make(chan struct{})}

		for i := start; i < end; i++ {
			r.slots[i] = entry
		}

		r.mu.Unlock()

		return s, true
	}
}

// release frees slots for the given range and signals waiters blocked in
// getOrCreate. Must be called exactly once per isNew=true return.
func (r *regionLock) release(chunkOff, chunkLen int64) {
	start, end := slotRange(chunkOff, chunkLen)

	r.mu.Lock()

	entry := r.slots[start]

	for i := start; i < end; i++ {
		r.slots[i] = nil
	}

	r.mu.Unlock()

	if entry != nil {
		close(entry.done)
	}
}
