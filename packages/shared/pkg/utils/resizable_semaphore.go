package utils

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

type AdjustableSemaphore struct {
	s          *semaphore.Weighted
	maxCap     int64
	currentCap int64
	mu         sync.Mutex
}

func NewAdjustableSemaphore(currentCap, maxCap int64) *AdjustableSemaphore {
	currentCap = min(currentCap, maxCap)

	return &AdjustableSemaphore{
		s:          semaphore.NewWeighted(currentCap),
		maxCap:     maxCap,
		currentCap: currentCap,
	}
}

// Acquire will resize the semaphore to the maximum capacity if the requested capacity is greater than the current capacity.
// It will then acquire one slot from the semaphore.
// If you are downsizing the semaphore capacity, it can block until enough capacity is available.
func (s *AdjustableSemaphore) Acquire(ctx context.Context, targetCap int64) error {
	targetCap = min(s.maxCap, targetCap)

	s.mu.Lock()
	defer s.mu.Unlock()

	// We release the current locked slots (remining to max capacity) so we can acquire the target capacity.
	s.s.Release(s.maxCap - s.currentCap)

	// We lock the capacity so that the available capacity is the target capacity.
	// We acquire one more slot than the target capacity to account for the current slot.
	return s.s.Acquire(ctx, s.maxCap-targetCap+1)
}

// The release is not locked so the slots can be released when we are blocked on the resize in acquire.
func (s *AdjustableSemaphore) Release() {
	s.s.Release(1)
}
