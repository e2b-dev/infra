package block

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

type fetchSession struct {
	// chunk is what we are fetching, can be >= 1 block. chunkOff/chunkLen are absolute offsets in U-space.
	chunkOff int64
	chunkLen int64
	cache    *Cache

	mu   sync.Mutex
	cond sync.Cond // broadcast on progress; lazily initialized with mu

	fetchErr error
	done     bool // true once terminated (success or error)

	// bytesReady is the byte count (from chunkOff) up to which all blocks
	// are fully written and marked cached. Atomic so registerAndWait can
	// do a lock-free fast-path check: bytesReady only increases.
	bytesReady atomic.Int64
}

// terminated reports whether the session reached a terminal state.
// Must be called with mu held.
func (s *fetchSession) terminated() bool {
	return s.done
}

func newFetchSession(chunkOff, chunkLen int64, cache *Cache) *fetchSession {
	s := &fetchSession{
		chunkOff: chunkOff,
		chunkLen: chunkLen,
		cache:    cache,
	}
	s.cond.L = &s.mu

	return s
}

// registerAndWait blocks until the block at blockOff is cached, the session
// terminates, or ctx is cancelled. Each caller requests exactly one block.
func (s *fetchSession) registerAndWait(ctx context.Context, blockOff int64) error {
	blockSize := s.cache.blockSize

	// endByte is the byte offset (relative to chunkOff) that must be ready
	// for our block to be fully written.
	relEnd := blockOff + blockSize - s.chunkOff
	endByte := min(relEnd, s.chunkLen)

	// Lock-free fast path: bytesReady only increases, so >= endByte
	// guarantees data is available.
	if s.bytesReady.Load() >= endByte {
		return nil
	}

	// Set up context cancellation to unblock cond.Wait.
	stop := context.AfterFunc(ctx, func() {
		s.cond.Broadcast()
	})
	defer stop()

	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		if s.bytesReady.Load() >= endByte {
			return nil
		}

		// Terminal but block not covered — only happens on error.
		// setDone sets bytesReady=chunkLen, so terminated() with bytesReady < endByte
		// means fetchErr != nil. Check cache in case a prior session already fetched this block.
		if s.terminated() {
			// isCached reads an atomic bitset — safe to call under mu.
			if s.cache.isCached(blockOff, blockSize) {
				return nil
			}

			if s.fetchErr == nil {
				return fmt.Errorf("fetch session terminated without error but block %d not cached (bytesReady=%d, endByte=%d)",
					blockOff/blockSize, s.bytesReady.Load(), endByte)
			}

			return fmt.Errorf("fetch failed: %w", s.fetchErr)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		s.cond.Wait()
	}
}

// advance updates progress and wakes all waiters.
func (s *fetchSession) advance(bytesReady int64) {
	s.bytesReady.Store(bytesReady)
	s.cond.Broadcast()
}

// setDone marks the session as successfully completed and wakes all waiters.
func (s *fetchSession) setDone() {
	s.mu.Lock()
	s.bytesReady.Store(s.chunkLen)
	s.done = true
	s.mu.Unlock()

	s.cond.Broadcast()
}

// setError records the error and wakes all waiters.
// When onlyIfRunning is true, it is a no-op if the session already
// terminated (used for panic recovery to avoid overriding a successful
// completion).
func (s *fetchSession) setError(err error, onlyIfRunning bool) {
	s.mu.Lock()
	if onlyIfRunning && s.terminated() {
		s.mu.Unlock()

		return
	}

	s.fetchErr = err
	s.done = true
	s.mu.Unlock()

	s.cond.Broadcast()
}
