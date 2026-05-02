package userfaultfd

// RPC service implementations for the cross-process UFFD test harness.
// These live in _test.go (rather than testutils/testharness) because they
// need access to unexported *Userfaultfd internals.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils/testharness"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// harnessState is the per-child container shared by Lifecycle / Paging /
// Barriers RPCs. Mutable fields are guarded by mu.
//
//nolint:containedctx // shutdown-aware ctx is shared with RPC handlers; lifetime is the child process.
type harnessState struct {
	uffdFd uintptr

	mu     sync.Mutex
	uffd   *Userfaultfd
	br     *testharness.Registry
	stop   func() // serve-stop fn; nil when paused
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
}

func newHarnessState(uffdFd uintptr) *harnessState {
	ctx, cancel := context.WithCancel(context.Background())

	return &harnessState{
		uffdFd: uffdFd,
		ctx:    ctx,
		cancel: cancel,
	}
}

// startServeLocked spawns the uffd Serve goroutine and stores its
// stop fn. Caller must hold s.mu. Idempotent: if a serve goroutine is
// already running (s.stop != nil), a stray duplicate Resume cannot
// leak an untracked Serve goroutine and break later pauses.
func (s *harnessState) startServeLocked() error {
	if s.stop != nil {
		return nil
	}

	exit, err := fdexit.New()
	if err != nil {
		return fmt.Errorf("fdexit.New: %w", err)
	}

	uffd := s.uffd
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := uffd.Serve(context.Background(), exit); err != nil {
			fmt.Fprintln(os.Stderr, "uffd.Serve:", err)
		}
	}()

	s.stop = func() {
		_ = exit.SignalExit()
		<-done
		exit.Close()
	}

	return nil
}

func (s *harnessState) stopServe() {
	// Snapshot s.stop and clear it under the lock, then drop the lock
	// before calling stop() — stop() blocks on <-done draining the Serve
	// goroutine, and any concurrent RPC handler that needs s.mu
	// (Barriers.registry, Paging.States/Resume, Lifecycle.Bootstrap/Shutdown)
	// would otherwise be blocked for the full drain. Required for the
	// upcoming barrier-race RPCs where WaitFaultHeld must run while a
	// worker parked at a barrier holds Pause's drain.
	s.mu.Lock()
	stop := s.stop
	s.stop = nil
	s.mu.Unlock()

	if stop != nil {
		stop()
	}
}

func (s *harnessState) releaseAllBarriers() {
	s.mu.Lock()
	br := s.br
	s.mu.Unlock()
	if br != nil {
		br.ReleaseAll()
	}
}

// Lifecycle owns boot/shutdown of the in-child uffd. Shutdown signals
// crossProcessServe to exit, where the serve goroutine and barrier
// registry are torn down outside any mutex.
type Lifecycle struct {
	state *harnessState
}

func (l *Lifecycle) Bootstrap(args *testharness.BootstrapArgs, _ *testharness.BootstrapReply) error {
	if int64(len(args.Content)) != args.TotalSize {
		return fmt.Errorf("content size %d != expected %d", len(args.Content), args.TotalSize)
	}

	data := NewMemorySlicer(args.Content, args.Pagesize)

	mapping := memory.NewMapping([]memory.Region{
		{
			BaseHostVirtAddr: uintptr(args.MmapStart),
			Size:             uintptr(args.TotalSize),
			Offset:           0,
			PageSize:         uintptr(args.Pagesize),
		},
	})

	log, err := logger.NewDevelopmentLogger()
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}

	uffd, err := NewUserfaultfdFromFd(l.state.uffdFd, data, mapping, log)
	if err != nil {
		return fmt.Errorf("NewUserfaultfdFromFd: %w", err)
	}

	if args.AlwaysWP {
		uffd.defaultCopyMode = UFFDIO_COPY_MODE_WP
	}

	var br *testharness.Registry
	if args.Barriers {
		br = testharness.NewRegistry()
		hook := br.Hook()
		uffd.SetTestFaultHook(func(addr uintptr, p faultPhase) {
			hook(addr, testharness.Point(p))
		})
	}

	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	l.state.uffd = uffd
	l.state.br = br

	return l.state.startServeLocked()
}

// WaitReady is a no-op today: Bootstrap is synchronous so its reply
// already implies readiness. Kept as a separate RPC so an async
// Bootstrap variant can hold the parent here without changing callers.
func (l *Lifecycle) WaitReady(_ *testharness.Empty, _ *testharness.Empty) error {
	return nil
}

func (l *Lifecycle) Shutdown(_ *testharness.Empty, _ *testharness.Empty) error {
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	if !l.state.closed {
		l.state.closed = true
		l.state.cancel()
	}

	return nil
}

// Paging exposes page-state introspection and pause/resume controls.
type Paging struct {
	state *harnessState
}

func (p *Paging) States(_ *testharness.Empty, reply *testharness.PageStatesReply) error {
	p.state.mu.Lock()
	uffd := p.state.uffd
	p.state.mu.Unlock()
	if uffd == nil {
		return errors.New("Paging.States called before Lifecycle.Bootstrap")
	}

	entries, err := uffd.pageStateEntries()
	if err != nil {
		return err
	}
	reply.Entries = entries

	return nil
}

func (p *Paging) Pause(_ *testharness.Empty, _ *testharness.Empty) error {
	p.state.stopServe()

	return nil
}

func (p *Paging) Resume(_ *testharness.Empty, _ *testharness.Empty) error {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()

	return p.state.startServeLocked()
}

// pageStateEntries returns a snapshot of every tracked page and its state in
// testharness wire format. Holds settleRequests.Lock to fence fault workers
// (mirrors PrefetchData) plus pageTracker.mu.RLock defensively, so a future
// writer that mutates pageTracker.m without going through settleRequests
// (e.g. a REMOVE event handler) cannot race this snapshot.
func (u *Userfaultfd) pageStateEntries() ([]testharness.PageStateEntry, error) {
	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

	u.pageTracker.mu.RLock()
	defer u.pageTracker.mu.RUnlock()

	entries := make([]testharness.PageStateEntry, 0, len(u.pageTracker.m))
	for addr, state := range u.pageTracker.m {
		offset, err := u.ma.GetOffset(addr)
		if err != nil {
			return nil, fmt.Errorf("address %#x not in mapping: %w", addr, err)
		}
		entries = append(entries, testharness.PageStateEntry{State: uint8(state), Offset: uint64(offset)})
	}

	return entries, nil
}

// Barriers is the thin RPC wrapper exposing testharness.Registry to
// the parent; locking and lifecycle live in testharness.
type Barriers struct {
	state *harnessState
}

func (b *Barriers) Install(args *testharness.FaultBarrierArgs, reply *testharness.FaultBarrierReply) error {
	br, err := b.registry()
	if err != nil {
		return err
	}
	reply.Token = br.Install(uintptr(args.Addr), testharness.Point(args.Point))

	return nil
}

func (b *Barriers) WaitHeld(args *testharness.TokenArgs, _ *testharness.Empty) error {
	br, err := b.registry()
	if err != nil {
		return err
	}

	return br.WaitArrived(b.state.ctx, args.Token)
}

func (b *Barriers) Release(args *testharness.TokenArgs, _ *testharness.Empty) error {
	br, err := b.registry()
	if err != nil {
		return err
	}
	br.Release(args.Token)

	return nil
}

func (b *Barriers) registry() (*testharness.Registry, error) {
	b.state.mu.Lock()
	br := b.state.br
	b.state.mu.Unlock()
	if br == nil {
		return nil, errors.New("Barriers RPC requires args.Barriers=true at Bootstrap")
	}

	return br, nil
}
