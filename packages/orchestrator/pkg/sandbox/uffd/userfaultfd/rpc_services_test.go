package userfaultfd

// RPC service implementations for the cross-process UFFD test
// harness. These live in _test.go (rather than the sibling
// internal/rpcharness package) because they need access to the
// unexported pageState / pageStateEntries / settleRequests /
// pageTracker / defaultCopyMode internals on *Userfaultfd. The wire
// types, typed Client, and barrier registry they consume are exported
// from internal/rpcharness so they cannot leak into a production
// import path.
//
// Three services are registered against the same *harnessState:
//
//   Lifecycle.Bootstrap / WaitReady / Shutdown
//   Paging.States / Pause / Resume
//   Barriers.Install / WaitHeld / Release

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd/internal/rpcharness"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// harnessState is the per-child container holding the resources
// created by Lifecycle.Bootstrap and consumed by Paging/Barriers.
// All three RPC services hold a *harnessState rather than duplicating
// fields. Mutable fields are guarded by mu.
type harnessState struct {
	uffdFd uintptr

	mu       sync.Mutex
	uffd     *Userfaultfd
	br       *rpcharness.Registry
	stop     func() // currently active serve-stop function, nil if paused
	shutdown chan struct{}
	closed   bool
}

func newHarnessState(uffdFd uintptr) *harnessState {
	return &harnessState{
		uffdFd:   uffdFd,
		shutdown: make(chan struct{}),
	}
}

// startServeLocked spawns the uffd Serve goroutine and stores its
// stop fn. Caller must hold s.mu. Idempotent: if the serve goroutine
// is already running (s.stop != nil) this is a no-op so a stray
// duplicate Resume can't leak an untracked Serve goroutine and break
// later pauses.
func (s *harnessState) startServeLocked() {
	if s.stop != nil {
		return
	}

	exit, err := fdexit.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fdexit.New:", err)

		return
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
}

func (s *harnessState) stopServe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		s.stop()
		s.stop = nil
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

// Lifecycle owns the boot/shutdown of the in-child uffd. Bootstrap
// builds the *Userfaultfd, optionally installs the test hooks, and
// kicks off the initial Serve goroutine. Shutdown signals the
// crossProcessServe loop to exit; the goroutine + barrier registry
// are torn down there so we don't have to hold any mutex while
// joining the serve goroutine.
type Lifecycle struct {
	state *harnessState
}

func (l *Lifecycle) Bootstrap(args *rpcharness.BootstrapArgs, _ *rpcharness.BootstrapReply) error {
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

	br := rpcharness.NewRegistry()
	if args.Barriers {
		uffd.SetTestHooks(&testHooks{
			beforeWorkerRLock: br.HookFor(rpcharness.BeforeRLock),
			beforeFaultPage:   br.HookFor(rpcharness.BeforeFaultPage),
		})
	}

	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	l.state.uffd = uffd
	l.state.br = br
	l.state.startServeLocked()

	return nil
}

// WaitReady is a no-op: Bootstrap is synchronous, so its successful
// reply already implies readiness. WaitReady is kept as a separate
// RPC so that an async-Bootstrap variant can be slotted in later
// without touching every call site.
func (l *Lifecycle) WaitReady(_ *rpcharness.Empty, _ *rpcharness.Empty) error {
	return nil
}

func (l *Lifecycle) Shutdown(_ *rpcharness.Empty, _ *rpcharness.Empty) error {
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	if !l.state.closed {
		l.state.closed = true
		close(l.state.shutdown)
	}

	return nil
}

// Paging is the RPC service exposing page-state introspection and
// the gated-serve pause/resume controls.
type Paging struct {
	state *harnessState
}

func (p *Paging) States(_ *rpcharness.Empty, reply *rpcharness.PageStatesReply) error {
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

func (p *Paging) Pause(_ *rpcharness.Empty, _ *rpcharness.Empty) error {
	p.state.stopServe()

	return nil
}

func (p *Paging) Resume(_ *rpcharness.Empty, _ *rpcharness.Empty) error {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.startServeLocked()

	return nil
}

// pageStateEntries returns a snapshot of every tracked page and its
// state, translated into the rpcharness wire format. Briefly takes
// settleRequests.Lock so no in-flight worker can mutate the
// pageTracker while we read it.
func (u *Userfaultfd) pageStateEntries() ([]rpcharness.PageStateEntry, error) {
	u.settleRequests.Lock()
	u.settleRequests.Unlock() //nolint:staticcheck // SA2001: intentional — settle the read locks.

	u.pageTracker.mu.RLock()
	defer u.pageTracker.mu.RUnlock()

	entries := make([]rpcharness.PageStateEntry, 0, len(u.pageTracker.m))
	for addr, state := range u.pageTracker.m {
		offset, err := u.ma.GetOffset(addr)
		if err != nil {
			return nil, fmt.Errorf("address %#x not in mapping: %w", addr, err)
		}
		entries = append(entries, rpcharness.PageStateEntry{State: uint8(state), Offset: uint64(offset)})
	}

	return entries, nil
}

// Barriers is the RPC service exposing the rpcharness.Registry to the
// parent. It's a thin wrapper so all the locking/lifecycle logic stays
// in rpcharness.
type Barriers struct {
	state *harnessState
}

func (b *Barriers) Install(args *rpcharness.FaultBarrierArgs, reply *rpcharness.FaultBarrierReply) error {
	br, err := b.registry()
	if err != nil {
		return err
	}
	reply.Token = br.Install(uintptr(args.Addr), rpcharness.Point(args.Point))

	return nil
}

func (b *Barriers) WaitHeld(args *rpcharness.TokenArgs, _ *rpcharness.Empty) error {
	br, err := b.registry()
	if err != nil {
		return err
	}

	return br.WaitArrived(context.Background(), args.Token)
}

func (b *Barriers) Release(args *rpcharness.TokenArgs, _ *rpcharness.Empty) error {
	br, err := b.registry()
	if err != nil {
		return err
	}
	br.Release(args.Token)

	return nil
}

func (b *Barriers) registry() (*rpcharness.Registry, error) {
	b.state.mu.Lock()
	br := b.state.br
	b.state.mu.Unlock()
	if br == nil {
		return nil, errors.New("Barriers RPC called before Lifecycle.Bootstrap")
	}

	return br, nil
}
