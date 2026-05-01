package userfaultfd

// Barriers RPC service plus the in-child barrierRegistry it
// manipulates. The hooks installed on Userfaultfd consult this
// registry by (addr, point) to decide whether to park a worker
// goroutine; the parent drives Install / WaitHeld / Release over RPC
// to deterministically race events against an in-flight worker.

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type FaultBarrierArgs struct {
	Addr  uint64
	Point uint8
}

type FaultBarrierReply struct {
	Token uint64
}

type TokenArgs struct {
	Token uint64
}

// Barriers is the RPC service exposing the barrier registry to the
// parent.
type Barriers struct {
	state *harnessState
}

func (b *Barriers) Install(args *FaultBarrierArgs, reply *FaultBarrierReply) error {
	br, err := b.registry()
	if err != nil {
		return err
	}
	reply.Token = br.install(uintptr(args.Addr), barrierPoint(args.Point))

	return nil
}

func (b *Barriers) WaitHeld(args *TokenArgs, _ *Empty) error {
	br, err := b.registry()
	if err != nil {
		return err
	}

	return br.waitArrived(context.Background(), args.Token)
}

func (b *Barriers) Release(args *TokenArgs, _ *Empty) error {
	br, err := b.registry()
	if err != nil {
		return err
	}
	br.release(args.Token)

	return nil
}

func (b *Barriers) registry() (*barrierRegistry, error) {
	b.state.mu.Lock()
	br := b.state.br
	b.state.mu.Unlock()
	if br == nil {
		return nil, errors.New("Barriers RPC called before Lifecycle.Bootstrap")
	}

	return br, nil
}

// barrierPoint identifies WHICH hook a barrier should park on.
type barrierPoint uint8

const (
	// barrierBeforeRLock parks the worker BEFORE settleRequests.RLock(),
	// i.e. before it can read the page state. Use this when a parallel
	// writer needs the write lock immediately because no worker holds
	// the read lock.
	barrierBeforeRLock barrierPoint = 1
	// barrierBeforeFaultPage parks the worker AFTER it has taken
	// settleRequests.RLock, but BEFORE the actual UFFDIO_COPY syscall.
	// Use this when a parent operation must still return even though
	// a worker holds RLock.
	barrierBeforeFaultPage barrierPoint = 2
)

// barrierRegistry is the child-process side of the barrier. The
// hooks installed on Userfaultfd consult this registry by addr+point
// to decide whether to park, and the RPC handlers manipulate it from
// the parent over the socket.
type barrierRegistry struct {
	mu     sync.Mutex
	next   uint64
	tokens map[uint64]*barrierSlot
	byKey  map[barrierKey]uint64
}

type barrierKey struct {
	addr  uintptr
	point barrierPoint
}

type barrierSlot struct {
	addr        uintptr
	point       barrierPoint
	arrived     chan struct{}
	release     chan struct{}
	arrivedOnce sync.Once
}

func newBarrierRegistry() *barrierRegistry {
	return &barrierRegistry{
		tokens: make(map[uint64]*barrierSlot),
		byKey:  make(map[barrierKey]uint64),
	}
}

func (b *barrierRegistry) install(addr uintptr, point barrierPoint) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.next++
	token := b.next
	slot := &barrierSlot{
		addr:    addr,
		point:   point,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
	b.tokens[token] = slot
	b.byKey[barrierKey{addr, point}] = token

	return token
}

func (b *barrierRegistry) lookupByAddr(addr uintptr, point barrierPoint) *barrierSlot {
	b.mu.Lock()
	defer b.mu.Unlock()

	token, ok := b.byKey[barrierKey{addr, point}]
	if !ok {
		return nil
	}

	return b.tokens[token]
}

func (b *barrierRegistry) waitArrived(ctx context.Context, token uint64) error {
	b.mu.Lock()
	slot, ok := b.tokens[token]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown barrier token %d", token)
	}

	select {
	case <-slot.arrived:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *barrierRegistry) release(token uint64) {
	b.mu.Lock()
	slot, ok := b.tokens[token]
	delete(b.tokens, token)
	if ok {
		delete(b.byKey, barrierKey{slot.addr, slot.point})
	}
	b.mu.Unlock()

	if !ok {
		return
	}

	select {
	case <-slot.release:
	default:
		close(slot.release)
	}
}

func (b *barrierRegistry) releaseAll() {
	b.mu.Lock()
	tokens := make([]uint64, 0, len(b.tokens))
	for t := range b.tokens {
		tokens = append(tokens, t)
	}
	b.mu.Unlock()

	for _, t := range tokens {
		b.release(t)
	}
}

// hookFor returns the function to assign to a Userfaultfd
// beforeXxxHook field. The returned function is a no-op for any
// (addr, point) pair that hasn't been Install'd, so non-targeted
// faults see no scheduling distortion.
func (b *barrierRegistry) hookFor(point barrierPoint) func(addr uintptr) {
	return func(addr uintptr) {
		slot := b.lookupByAddr(addr, point)
		if slot == nil {
			return
		}

		slot.arrivedOnce.Do(func() {
			close(slot.arrived)
		})

		<-slot.release
	}
}
