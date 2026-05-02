package testharness

import (
	"context"
	"fmt"
	"sync"
)

// Point identifies WHICH worker hook a barrier should park on. Values
// must match the parent package's faultPhase iota so the test hook can
// pass them through with a numeric cast.
type Point uint8

const (
	// BeforeRLock parks the worker BEFORE settleRequests.RLock(), so a
	// parallel writer can take the write lock immediately.
	BeforeRLock Point = iota
	// BeforeFaultPage parks the worker AFTER settleRequests.RLock but
	// BEFORE the UFFDIO_COPY syscall, so a parent operation must still
	// return even though a worker holds RLock.
	BeforeFaultPage
)

// Registry is the child-process side of the barrier mechanism. The
// per-fault hook on *Userfaultfd consults it by (addr, point) to decide
// whether to park.
type Registry struct {
	mu     sync.Mutex
	next   uint64
	tokens map[uint64]*slot
	byKey  map[key]uint64
}

type key struct {
	addr  uintptr
	point Point
}

type slot struct {
	addr        uintptr
	point       Point
	arrived     chan struct{}
	release     chan struct{}
	arrivedOnce sync.Once
}

func NewRegistry() *Registry {
	return &Registry{
		tokens: make(map[uint64]*slot),
		byKey:  make(map[key]uint64),
	}
}

// Install registers a barrier at (addr, point) and returns its token.
func (r *Registry) Install(addr uintptr, point Point) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.next++
	token := r.next
	s := &slot{
		addr:    addr,
		point:   point,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
	r.tokens[token] = s
	r.byKey[key{addr, point}] = token

	return token
}

func (r *Registry) lookupByAddr(addr uintptr, point Point) *slot {
	r.mu.Lock()
	defer r.mu.Unlock()

	token, ok := r.byKey[key{addr, point}]
	if !ok {
		return nil
	}

	return r.tokens[token]
}

// WaitArrived blocks until the worker hook for token has reached the
// barrier point, or until ctx is cancelled.
func (r *Registry) WaitArrived(ctx context.Context, token uint64) error {
	r.mu.Lock()
	s, ok := r.tokens[token]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown barrier token %d", token)
	}

	select {
	case <-s.arrived:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees the barrier identified by token, allowing any parked
// worker to proceed. Releasing an unknown token is a no-op.
func (r *Registry) Release(token uint64) {
	r.mu.Lock()
	s, ok := r.tokens[token]
	delete(r.tokens, token)
	if ok {
		delete(r.byKey, key{s.addr, s.point})
	}
	r.mu.Unlock()

	if !ok {
		return
	}

	select {
	case <-s.release:
	default:
		close(s.release)
	}
}

// ReleaseAll releases every still-installed barrier so any parked
// worker can finish before the child's serve goroutine is joined.
func (r *Registry) ReleaseAll() {
	r.mu.Lock()
	tokens := make([]uint64, 0, len(r.tokens))
	for t := range r.tokens {
		tokens = append(tokens, t)
	}
	r.mu.Unlock()

	for _, t := range tokens {
		r.Release(t)
	}
}

// Hook returns the per-fault hook tests install on *Userfaultfd. Faults
// at (addr, point) pairs without an Install'd slot are no-ops.
func (r *Registry) Hook() func(addr uintptr, point Point) {
	return func(addr uintptr, point Point) {
		s := r.lookupByAddr(addr, point)
		if s == nil {
			return
		}

		s.arrivedOnce.Do(func() {
			close(s.arrived)
		})

		<-s.release
	}
}
