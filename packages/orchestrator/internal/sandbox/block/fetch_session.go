package block

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

type fetchSession struct {
	// chunk is what we are fetching, can be >= 1 block. chunkOff/chunkLen are absolute offsets in U-space.
	chunkOff  int64
	chunkLen  int64
	blockSize int64

	mu       sync.Mutex
	fetchErr error
	signal   chan struct{} // closed on each advance; nil when terminated

	// bytesReady is the byte count (from chunkOff) up to which all blocks
	// are fully written and marked cached. Atomic so registerAndWait can
	// do a lock-free fast-path check: bytesReady only increases.
	bytesReady atomic.Int64

	// isCachedFn checks persistent cache for data from previous sessions.
	isCachedFn func(off, length int64) bool
}

// terminated reports whether the session reached a terminal state.
// Must be called with mu held.
func (s *fetchSession) terminated() bool {
	return s.fetchErr != nil || s.bytesReady.Load() == s.chunkLen
}

func newFetchSession(chunkOff, chunkLen, blockSize int64, isCachedFn func(off, length int64) bool) *fetchSession {
	return &fetchSession{
		chunkOff:   chunkOff,
		chunkLen:   chunkLen,
		blockSize:  blockSize,
		isCachedFn: isCachedFn,
		signal:     make(chan struct{}),
	}
}

// registerAndWait blocks until [off, off+length) is cached, the session
// terminates, or ctx is cancelled.
func (s *fetchSession) registerAndWait(ctx context.Context, off, length int64) error {
	relEnd := off + length - s.chunkOff

	var endByte int64
	if s.blockSize > 0 {
		lastBlockIdx := (relEnd - 1) / s.blockSize
		endByte = min((lastBlockIdx+1)*s.blockSize, s.chunkLen)
	} else {
		endByte = s.chunkLen
	}

	for {
		// Lock-free fast path: bytesReady only increases, so >= endByte
		// guarantees data is available.
		if s.bytesReady.Load() >= endByte {
			return nil
		}

		s.mu.Lock()

		// Re-check under lock.
		if s.bytesReady.Load() >= endByte {
			s.mu.Unlock()

			return nil
		}

		// Terminal but range not covered — only happens on error
		// (setDone sets bytesReady=chunkLen). Check cache for prior session data.
		if s.terminated() {
			fetchErr := s.fetchErr
			s.mu.Unlock()

			if s.isCachedFn != nil && s.isCachedFn(off, length) {
				return nil
			}

			if fetchErr != nil {
				return fmt.Errorf("fetch failed: %w", fetchErr)
			}

			return nil
		}

		ch := s.signal
		s.mu.Unlock()

		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// advance updates progress and wakes all waiters by closing the current
// broadcast channel and replacing it with a fresh one.
func (s *fetchSession) advance(bytesReady int64) {
	s.mu.Lock()
	s.bytesReady.Store(bytesReady)
	old := s.signal
	s.signal = make(chan struct{})
	s.mu.Unlock()

	close(old)
}

// setDone marks the session as successfully completed and wakes all waiters.
func (s *fetchSession) setDone() {
	s.mu.Lock()
	s.bytesReady.Store(s.chunkLen)
	old := s.signal
	s.signal = nil
	s.mu.Unlock()

	close(old)
}

// setError records the error and wakes all waiters.
// When onlyIfRunning is true, it is a no-op if the session already
// terminated (used for panic recovery to avoid overriding a successful
// completion or double-closing the broadcast channel).
func (s *fetchSession) setError(err error, onlyIfRunning bool) {
	s.mu.Lock()
	if onlyIfRunning && s.terminated() {
		s.mu.Unlock()

		return
	}

	s.fetchErr = err
	old := s.signal
	s.signal = nil
	s.mu.Unlock()

	if old != nil {
		close(old)
	}
}
