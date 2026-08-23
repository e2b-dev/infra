//go:build linux

package userfaultfd

// RPC service implementations for the cross-process UFFD test harness;
// in _test.go because they need *Userfaultfd internals.

import (
	"bytes"
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

	// data is the bootstrap source content: the CoW window reads pre-images
	// from it (pages installed from source and not yet written hold exactly
	// this content, which is the only view of "guest memory" the serving
	// child has — the parent's mmap is not mapped here).
	data *MemorySlicer
	// window/sink hold the active CoW export driven over RPC.
	window *CoWWindow
	sink   *cowHarnessSink
}

// cowHarnessSink is a lockable identity-offset WriterAt over a byte slice.
type cowHarnessSink struct {
	mu  sync.Mutex
	buf []byte
}

func (s *cowHarnessSink) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return copy(s.buf[off:], p), nil
}

func (s *cowHarnessSink) snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]byte(nil), s.buf...)
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

	uffd, err := NewUserfaultfdFromFd(l.state.uffdFd, data, mapping, 0, log)
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
	l.state.data = data

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

	bmDirty, bmZero, bmRemoved, bmClean := u.pageTracker.ExportStates()
	entries := make([]testharness.PageStateEntry, 0,
		bmDirty.GetCardinality()+bmZero.GetCardinality()+bmRemoved.GetCardinality()+bmClean.GetCardinality())
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
	emit(bmRemoved, block.Removed)
	emit(bmClean, block.Clean)

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

// CoW drives a real CoW export window through the live serve loop: Begin
// arms + installs via BeginCoWExport, State snapshots progress/cancellation
// and the sink, Sweep drains and uninstalls via EndCoWExport — exercising the
// real readSerial/settleRequests interplay the isolated CoWWindow tests
// cannot.
type CoW struct {
	state *harnessState
}

func (c *CoW) Begin(args *testharness.CoWBeginArgs, _ *testharness.Empty) error {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	uffd := c.state.uffd
	if uffd == nil {
		return errors.New("CoW.Begin called before Lifecycle.Bootstrap")
	}
	if c.state.window != nil {
		return errors.New("CoW.Begin: a window is already installed")
	}

	pages := roaring.New()
	for _, p := range args.Pages {
		pages.Add(uint32(p))
	}

	size, err := c.state.data.Size()
	if err != nil {
		return err
	}
	sink := &cowHarnessSink{buf: make([]byte, size)}

	// Pre-images come from the bootstrap source: correct for pages installed
	// from source and not yet written, which is what the parent tests use.
	src := bytes.NewReader(c.state.data.Content())
	w, err := uffd.BeginCoWExport(pages, src, sink)
	if err != nil {
		return err
	}
	c.state.window = w
	c.state.sink = sink

	return nil
}

func (c *CoW) State(_ *testharness.Empty, reply *testharness.CoWStateReply) error {
	c.state.mu.Lock()
	w, sink := c.state.window, c.state.sink
	c.state.mu.Unlock()
	if w == nil {
		return errors.New("CoW.State: no window installed")
	}

	reply.Copied = w.Copied()
	if cause := w.CancelCause(); cause != nil {
		reply.Canceled = true
		reply.CancelCause = cause.Error()
	}
	reply.Sink = sink.snapshot()

	return nil
}

func (c *CoW) Sweep(_ *testharness.Empty, reply *testharness.CoWSweepReply) error {
	c.state.mu.Lock()
	w := c.state.window
	uffd := c.state.uffd
	c.state.mu.Unlock()
	if w == nil {
		return errors.New("CoW.Sweep: no window installed")
	}

	err := w.Sweep(context.Background())
	uffd.EndCoWExport(w)
	if err == nil {
		err = w.CancelCause()
	}
	if err != nil {
		reply.SweepError = err.Error()
	}
	if cause := w.CancelCause(); cause != nil {
		reply.CancelCause = cause.Error()
	}

	c.state.mu.Lock()
	c.state.window, c.state.sink = nil, nil
	c.state.mu.Unlock()

	return nil
}
