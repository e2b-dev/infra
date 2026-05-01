package userfaultfd

// Lifecycle RPC service plus the harnessState container that the
// three RPC services share. Lifecycle.Bootstrap is the single point
// where the in-child *Userfaultfd is constructed; Paging and
// Barriers consume the resulting state via *harnessState.

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Empty is the stand-in args/reply type for net/rpc methods that
// take or return nothing meaningful. net/rpc still requires both
// pointers to be exported.
type Empty struct{}

// harnessState is the per-child container holding the resources
// created by Lifecycle.Bootstrap and consumed by Paging/Barriers.
// All three RPC services hold a *harnessState rather than
// duplicating fields. Mutable fields are guarded by mu.
type harnessState struct {
	uffdFd uintptr

	mu       sync.Mutex
	uffd     *Userfaultfd
	br       *barrierRegistry
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
		br.releaseAll()
	}
}

// BootstrapArgs is the single message the parent sends to drive
// child initialisation. Everything that used to flow over env vars
// or the content tmp file now lives in this struct, base64-encoded
// by the JSON-RPC codec for byte slices. For typical test sizes
// (≤1MB) the encoding overhead is irrelevant; if a future test needs
// >10MB of source content, that PR can revisit and add chunking.
type BootstrapArgs struct {
	MmapStart uint64
	Pagesize  int64
	TotalSize int64
	AlwaysWP  bool
	// Barriers gates the test-only worker hooks. Off by default so
	// the worker hot path stays a single nil-pointer load + branch
	// in non-race tests.
	Barriers bool
	Content  []byte
}

type BootstrapReply struct{}

// Lifecycle owns the boot/shutdown of the in-child uffd. Bootstrap
// builds the *Userfaultfd, optionally installs the test hooks, and
// kicks off the initial Serve goroutine. Shutdown signals the
// crossProcessServe loop to exit; the goroutine + barrier registry
// are torn down there so we don't have to hold any mutex while
// joining the serve goroutine.
type Lifecycle struct {
	state *harnessState
}

func (l *Lifecycle) Bootstrap(args *BootstrapArgs, _ *BootstrapReply) error {
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

	br := newBarrierRegistry()
	if args.Barriers {
		uffd.SetTestHooks(&testHooks{
			beforeWorkerRLock: br.hookFor(barrierBeforeRLock),
			beforeFaultPage:   br.hookFor(barrierBeforeFaultPage),
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
func (l *Lifecycle) WaitReady(_ *Empty, _ *Empty) error {
	return nil
}

func (l *Lifecycle) Shutdown(_ *Empty, _ *Empty) error {
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	if !l.state.closed {
		l.state.closed = true
		close(l.state.shutdown)
	}

	return nil
}
