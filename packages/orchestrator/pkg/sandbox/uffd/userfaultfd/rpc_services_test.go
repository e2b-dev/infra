package userfaultfd

// RPC service implementations for the cross-process UFFD test harness.
// These live in _test.go (rather than internal/rpcharness) because they
// need access to unexported *Userfaultfd internals.

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

// harnessState is the per-child container shared by Lifecycle / Paging /
// Barriers RPCs. Mutable fields are guarded by mu.
type harnessState struct {
	uffdFd uintptr

	mu       sync.Mutex
	uffd     *Userfaultfd
	br       *rpcharness.Registry
	stop     func() // serve-stop fn; nil when paused
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

// Lifecycle owns boot/shutdown of the in-child uffd. Shutdown signals
// crossProcessServe to exit, where the serve goroutine and barrier
// registry are torn down outside any mutex.
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
		hook := br.Hook()
		uffd.SetTestFaultHook(func(addr uintptr, p faultPhase) {
			hook(addr, rpcharness.Point(p))
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

// Paging exposes page-state introspection and pause/resume controls.
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

	return p.state.startServeLocked()
}

// pageStateEntries returns a snapshot of every tracked page and its state in
// rpcharness wire format. Holds settleRequests.Lock for the duration so no
// fault worker is in flight; mirrors PrefetchData.
func (u *Userfaultfd) pageStateEntries() ([]rpcharness.PageStateEntry, error) {
	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

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

// Barriers is the thin RPC wrapper exposing rpcharness.Registry to
// the parent; locking and lifecycle live in rpcharness.
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
