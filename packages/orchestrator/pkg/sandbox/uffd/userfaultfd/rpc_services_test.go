package userfaultfd

// RPC service implementations for the cross-process UFFD test harness;
// in _test.go because they need *Userfaultfd internals.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils/testharness"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

//nolint:containedctx // shutdown-aware ctx shared with RPC handlers; lifetime is the child process.
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

// startServeLocked is idempotent so a stray duplicate Resume cannot
// leak an untracked Serve goroutine. Caller must hold s.mu.
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
	// Drop s.mu before stop() — stop() blocks on the Serve drain, and any
	// concurrent RPC handler needing s.mu (e.g. WaitFaultHeld during a
	// parked barrier) would otherwise stall until the drain completes.
	s.mu.Lock()
	stop := s.stop
	s.stop = nil
	s.mu.Unlock()

	if stop != nil {
		stop()
	}
}

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

// WaitReady is a no-op today (Bootstrap is synchronous); kept as a separate
// RPC so an async-Bootstrap variant can hold the parent here unchanged.
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

// pageStateEntries returns a wire-format snapshot of pageTracker.
// settleRequests.Lock drains fault workers (mirrors PrefetchData) so
// the snapshot is consistent w.r.t. concurrent installs.
func (u *Userfaultfd) pageStateEntries() ([]testharness.PageStateEntry, error) {
	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

	bmDirty, bmZero := u.pageTracker.Export()
	entries := make([]testharness.PageStateEntry, 0, bmDirty.GetCardinality()+bmZero.GetCardinality())
	emit := func(bm *roaring.Bitmap, state block.State) {
		for _, idx := range bm.ToArray() {
			entries = append(entries, testharness.PageStateEntry{
				State:  uint8(state),
				Offset: uint64(idx) * uint64(u.pageSize),
			})
		}
	}
	emit(bmDirty, block.Dirty)
	emit(bmZero, block.Zero)

	return entries, nil
}

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
