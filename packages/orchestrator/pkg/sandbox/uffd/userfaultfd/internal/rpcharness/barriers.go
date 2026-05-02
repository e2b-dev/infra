package rpcharness

import (
	"context"
	"fmt"
	"sync"
)

// Point identifies WHICH worker hook a barrier should park on.
type Point uint8

const (
	// BeforeRLock parks the worker BEFORE settleRequests.RLock(),
	// i.e. before it can read the page state. Use this when a parallel
	// writer needs the write lock immediately because no worker holds
	// the read lock.
	BeforeRLock Point = 1
	// BeforeFaultPage parks the worker AFTER it has taken
	// settleRequests.RLock, but BEFORE the actual UFFDIO_COPY syscall.
	// Use this when a parent operation must still return even though a
	// worker holds RLock.
	BeforeFaultPage Point = 2
)

// Registry is the child-process side of the barrier mechanism. The
// hooks installed on *Userfaultfd consult this registry by addr+point
// to decide whether to park, and the Barriers RPC handlers manipulate
// it from the parent over the socket.
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

// Install registers a barrier at (addr, point) and returns the opaque
// token used by subsequent WaitArrived/Release calls.
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

// ReleaseAll releases every still-installed barrier. Called during
// child shutdown so that any parked worker can finish before the
// serve goroutine is joined.
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

// Hook returns the function tests assign as the per-fault hook on
// *Userfaultfd. The returned closure dispatches by (addr, point):
// pages/points that haven't been Install'd see no scheduling distortion.
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
