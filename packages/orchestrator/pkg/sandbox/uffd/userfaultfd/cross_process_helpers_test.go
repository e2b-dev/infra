package userfaultfd

// This test creates the userfaultfd in the parent test process and
// drives it from a child helper process. We do this so the actual
// page-fault handling runs in a process where we can fully control
// memory layout (no Go GC scanning / touching the registered region)
// — which mirrors how Firecracker uses UFFD in production.
//
// All parent ↔ child coordination — readiness, page-state queries,
// pause/resume, fault barriers, shutdown — flows over a single Unix
// domain socket using the JSON-RPC harness in rpc_test.go. The only
// fd we still hand off out-of-band is the userfaultfd itself, which
// is a kernel object and has to be passed via ExtraFiles. The
// initial source data is written to a temp file (path passed in an
// env var) because base64-stuffing megabytes through the JSON
// envelope would be silly.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// MemorySlicer exposes a byte slice via the Slicer interface.
// Test-only.
type MemorySlicer struct {
	content  []byte
	pagesize int64
}

var _ block.Slicer = (*MemorySlicer)(nil)

func NewMemorySlicer(content []byte, pagesize int64) *MemorySlicer {
	return &MemorySlicer{content: content, pagesize: pagesize}
}

func (s *MemorySlicer) Slice(_ context.Context, offset, size int64) ([]byte, error) {
	return s.content[offset : offset+size], nil
}

func (s *MemorySlicer) Size() (int64, error) {
	return int64(len(s.content)), nil
}

func (s *MemorySlicer) Content() []byte {
	return s.content
}

func (s *MemorySlicer) BlockSize() int64 {
	return s.pagesize
}

func RandomPages(pagesize, numberOfPages uint64) *MemorySlicer {
	size := pagesize * numberOfPages
	buf := make([]byte, int(size))
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return NewMemorySlicer(buf, int64(pagesize))
}

// Env vars used by the child helper process. Kept in one place so
// drift between parent (sets) and child (reads) is impossible.
const (
	envHelperFlag    = "GO_TEST_HELPER_PROCESS"
	envSocketPath    = "GO_UFFD_SOCKET"
	envContentPath   = "GO_UFFD_CONTENT"
	envMmapStart     = "GO_UFFD_MMAP_START"
	envMmapPagesize  = "GO_UFFD_MMAP_PAGESIZE"
	envMmapTotalSize = "GO_UFFD_MMAP_SIZE"
	envAlwaysWP      = "GO_UFFD_ALWAYS_WP"
	envGated         = "GO_UFFD_GATED"
)

// Main process, FC in our case
func configureCrossProcessTest(t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := RandomPages(tt.pagesize, tt.numberOfPages)

	if tt.sourcePatcher != nil {
		tt.sourcePatcher(data.Content())
	}

	size, err := data.Size()
	require.NoError(t, err)

	memoryArea, memoryStart, err := testutils.NewPageMmap(t, uint64(size), tt.pagesize)
	require.NoError(t, err)

	uffdFd, err := newFd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
	require.NoError(t, err)
	t.Cleanup(func() {
		uffdFd.close()
	})

	require.NoError(t, configureApi(uffdFd, tt.pagesize))
	require.NoError(t, register(uffdFd, memoryStart, uint64(size), UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP))

	t.Cleanup(func() {
		// Tear the registration down before the late close. With
		// UFFD_FEATURE_EVENT_REMOVE enabled (see configureApi),
		// munmap can otherwise block on un-acked REMOVE events.
		_ = unregister(uffdFd, memoryStart, uint64(size))
	})

	tmpDir := t.TempDir()

	contentPath := filepath.Join(tmpDir, "content.bin")
	require.NoError(t, os.WriteFile(contentPath, data.Content(), 0o600))

	socketPath := filepath.Join(tmpDir, "rpc.sock")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperServingProcess", "-test.timeout=0")
	cmd.Env = append(os.Environ(),
		envHelperFlag+"=1",
		envSocketPath+"="+socketPath,
		envContentPath+"="+contentPath,
		fmt.Sprintf("%s=%d", envMmapStart, memoryStart),
		fmt.Sprintf("%s=%d", envMmapPagesize, tt.pagesize),
		fmt.Sprintf("%s=%d", envMmapTotalSize, size),
	)
	if tt.alwaysWP {
		cmd.Env = append(cmd.Env, envAlwaysWP+"=1")
	}
	if tt.gated {
		cmd.Env = append(cmd.Env, envGated+"=1")
	}

	dup, err := syscall.Dup(int(uffdFd))
	require.NoError(t, err)
	if _, err := unix.FcntlInt(uintptr(dup), unix.F_SETFD, 0); err != nil {
		require.NoError(t, err)
	}

	uffdFile := os.NewFile(uintptr(dup), "uffd")
	cmd.ExtraFiles = []*os.File{uffdFile}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	require.NoError(t, cmd.Start())
	uffdFile.Close()

	// Accept the child's connection. Tight deadline so a wedged
	// child surfaces fast instead of hanging the test.
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		c, err := listener.Accept()
		acceptCh <- acceptResult{conn: c, err: err}
	}()

	var conn net.Conn
	select {
	case res := <-acceptCh:
		require.NoError(t, res.err)
		conn = res.conn
	case <-t.Context().Done():
		listener.Close()
		require.NoError(t, t.Context().Err())
	}
	listener.Close()

	client := newRPCClient(conn, conn)

	h := &testHandler{
		memoryArea: &memoryArea,
		pagesize:   tt.pagesize,
		data:       data,
		client:     client,
		conn:       conn,
		cmd:        cmd,
	}

	// WaitReady blocks on the child until its initial setup is done
	// (uffd resumed, hooks installed). This is the RPC equivalent of
	// the old "ready pipe + read until EOF" handshake.
	require.NoError(t, h.client.Call(t.Context(), "WaitReady", nil, nil))

	t.Cleanup(func() {
		// Best-effort graceful shutdown via RPC. If the child has
		// already crashed the RPC will error and we fall back to
		// killing the process via cmd.Process.Kill on the next line.
		_ = h.client.Call(context.Background(), "Shutdown", nil, nil)
		_ = conn.Close()

		// cmd.Wait can return ExitError, "signal: killed", or nil
		// depending on whether the child exited cleanly. Any of
		// those is acceptable here.
		waitErr := cmd.Wait()
		if waitErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(waitErr, &exitErr) {
				t.Logf("helper process Wait: %v", waitErr)
			}
		}
	})

	if tt.gated {
		h.servePause = func() error {
			return h.client.Call(t.Context(), "ServePause", nil, nil)
		}
		h.serveResume = func() error {
			return h.client.Call(t.Context(), "ServeResume", nil, nil)
		}
	}

	h.pageStatesOnce = func() (handlerPageStates, error) {
		var entries []pageStateEntry
		if err := h.client.Call(t.Context(), "PageStates", nil, &entries); err != nil {
			return handlerPageStates{}, err
		}

		var states handlerPageStates
		for _, e := range entries {
			switch pageState(e.State) {
			case faulted:
				states.faulted = append(states.faulted, uint(e.Offset))
			case removed:
				states.removed = append(states.removed, uint(e.Offset))
			}
		}
		slices.Sort(states.faulted)
		slices.Sort(states.removed)

		return states, nil
	}

	return h, nil
}

// Secondary process, orchestrator in our case.
func TestHelperServingProcess(t *testing.T) {
	t.Parallel()

	if os.Getenv(envHelperFlag) != "1" {
		t.Skip("this is a helper process, skipping direct execution")
	}

	if err := crossProcessServe(); err != nil {
		fmt.Fprintln(os.Stderr, "exit serving process:", err)
		os.Exit(1)
	}

	os.Exit(0)
}

// crossProcessServe wires up the child side: connects back to the
// parent socket, exposes the RPC surface, and runs uffd.Serve in a
// background goroutine that pause/resume RPCs can stop and restart.
func crossProcessServe() error {
	socketPath := os.Getenv(envSocketPath)
	if socketPath == "" {
		return fmt.Errorf("missing %s", envSocketPath)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial parent socket: %w", err)
	}
	defer conn.Close()

	startRaw, err := strconv.ParseUint(os.Getenv(envMmapStart), 10, 64)
	if err != nil {
		return fmt.Errorf("parse %s: %w", envMmapStart, err)
	}
	memoryStart := uintptr(startRaw)

	pagesize, err := strconv.ParseInt(os.Getenv(envMmapPagesize), 10, 64)
	if err != nil {
		return fmt.Errorf("parse %s: %w", envMmapPagesize, err)
	}

	totalSize, err := strconv.ParseInt(os.Getenv(envMmapTotalSize), 10, 64)
	if err != nil {
		return fmt.Errorf("parse %s: %w", envMmapTotalSize, err)
	}

	content, err := os.ReadFile(os.Getenv(envContentPath))
	if err != nil {
		return fmt.Errorf("read content: %w", err)
	}
	if int64(len(content)) != totalSize {
		return fmt.Errorf("content size %d != expected %d", len(content), totalSize)
	}

	data := NewMemorySlicer(content, pagesize)

	uffdFile := os.NewFile(uintptr(3), "uffd")
	defer uffdFile.Close()
	uffdFd := uffdFile.Fd()

	mapping := memory.NewMapping([]memory.Region{
		{
			BaseHostVirtAddr: memoryStart,
			Size:             uintptr(totalSize),
			Offset:           0,
			PageSize:         uintptr(pagesize),
		},
	})

	l, err := logger.NewDevelopmentLogger()
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}

	uffd, err := NewUserfaultfdFromFd(uffdFd, data, mapping, l)
	if err != nil {
		return fmt.Errorf("NewUserfaultfdFromFd: %w", err)
	}

	if os.Getenv(envAlwaysWP) == "1" {
		uffd.defaultCopyMode = UFFDIO_COPY_MODE_WP
	}

	// Wire the deterministic test barriers into the production hook
	// fields. Both hooks consult the same per-addr registry below.
	br := newBarrierRegistry()
	uffd.beforeWorkerRLockHook = br.hookFor(barrierBeforeRLock)
	uffd.beforeFaultPageHook = br.hookFor(barrierBeforeFaultPage)

	server := newRPCServer(conn, conn)

	serveCtx, serveCancel := context.WithCancel(context.Background())

	// Lifecycle of the actual UFFD serve loop. Pause/resume RPCs
	// stop and restart this goroutine; Shutdown signals the outer
	// loop to exit. We use a small helper to avoid duplicating the
	// "create fdexit + go uffd.Serve + wait" pattern.
	var serveMu sync.Mutex
	var serveStop func()

	startServe := func() {
		exit, err := fdexit.New()
		if err != nil {
			fmt.Fprintln(os.Stderr, "fdexit.New:", err)

			return
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := uffd.Serve(serveCtx, exit); err != nil {
				fmt.Fprintln(os.Stderr, "uffd.Serve:", err)
			}
		}()

		serveStop = func() {
			_ = exit.SignalExit()
			<-done
			exit.Close()
		}
	}

	startServe()

	gated := os.Getenv(envGated) == "1"

	// Track in-flight barrier tokens so Shutdown can release them
	// (otherwise a parked worker would never return and the serve
	// goroutine would never finish).
	server.Register("WaitReady", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, nil
	})

	server.Register("PageStates", func(_ context.Context, _ json.RawMessage) (any, error) {
		return uffd.pageStateEntries()
	})

	server.Register("ServePause", func(_ context.Context, _ json.RawMessage) (any, error) {
		if !gated {
			return nil, errors.New("ServePause called on a non-gated handler")
		}
		serveMu.Lock()
		defer serveMu.Unlock()
		if serveStop != nil {
			serveStop()
			serveStop = nil
		}

		return nil, nil
	})

	server.Register("ServeResume", func(_ context.Context, _ json.RawMessage) (any, error) {
		if !gated {
			return nil, errors.New("ServeResume called on a non-gated handler")
		}
		serveMu.Lock()
		defer serveMu.Unlock()
		startServe()

		return nil, nil
	})

	server.Register("InstallFaultBarrier", func(_ context.Context, raw json.RawMessage) (any, error) {
		var args installFaultBarrierArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		token := br.install(uintptr(args.Addr), barrierPoint(args.Point))

		return installFaultBarrierResp{Token: token}, nil
	})

	server.Register("WaitFaultHeld", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args tokenArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}

		return nil, br.waitArrived(ctx, args.Token)
	})

	server.Register("ReleaseFault", func(_ context.Context, raw json.RawMessage) (any, error) {
		var args tokenArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		br.release(args.Token)

		return nil, nil
	})

	shutdownCh := make(chan struct{})
	server.Register("Shutdown", func(_ context.Context, _ json.RawMessage) (any, error) {
		// Run the actual shutdown asynchronously so the RPC reply
		// goes out before we tear the channel down.
		go func() {
			close(shutdownCh)
		}()

		return nil, nil
	})

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve(serveCtx)
	}()

	select {
	case <-shutdownCh:
	case err := <-serveErrCh:
		if err != nil {
			fmt.Fprintln(os.Stderr, "rpc server:", err)
		}
	}

	serveCancel()

	// Release any still-parked barriers so the serve goroutine can
	// finish before we ask it to stop.
	br.releaseAll()

	serveMu.Lock()
	if serveStop != nil {
		serveStop()
		serveStop = nil
	}
	serveMu.Unlock()

	return nil
}

// pageStateEntry is the wire format for PageStates RPC results.
type pageStateEntry struct {
	State  uint8  `json:"state"`
	Offset uint64 `json:"offset"`
}

// pageStateEntries returns a snapshot of every tracked page and its
// state. It briefly takes settleRequests.Lock so no in-flight worker
// can mutate the pageTracker while we read it.
func (u *Userfaultfd) pageStateEntries() ([]pageStateEntry, error) {
	u.settleRequests.Lock()
	u.settleRequests.Unlock() //nolint:staticcheck // SA2001: intentional — settle the read locks.

	u.pageTracker.mu.RLock()
	defer u.pageTracker.mu.RUnlock()

	entries := make([]pageStateEntry, 0, len(u.pageTracker.m))
	for addr, state := range u.pageTracker.m {
		offset, err := u.ma.GetOffset(addr)
		if err != nil {
			return nil, fmt.Errorf("address %#x not in mapping: %w", addr, err)
		}
		entries = append(entries, pageStateEntry{State: uint8(state), Offset: uint64(offset)})
	}

	return entries, nil
}

// barrierPoint identifies WHICH hook a barrier should park on.
type barrierPoint uint8

const (
	// barrierBeforeRLock parks the worker BEFORE settleRequests.RLock(),
	// i.e. before it can read the page state. Use this for the
	// stale-source race: a parallel REMOVE batch on the parent loop
	// can take the write lock immediately because no worker holds
	// the read lock.
	barrierBeforeRLock barrierPoint = 1
	// barrierBeforeFaultPage parks the worker AFTER it has taken
	// settleRequests.RLock and decided on `source`, but BEFORE the
	// actual UFFDIO_COPY syscall. Use this for the in-flight COPY
	// deadlock test: the parent's madvise must still return even
	// though a worker holds RLock.
	barrierBeforeFaultPage barrierPoint = 2
)

// installFaultBarrierArgs is the InstallFaultBarrier RPC payload.
type installFaultBarrierArgs struct {
	Addr  uint64 `json:"addr"`
	Point uint8  `json:"point"`
}

type installFaultBarrierResp struct {
	Token uint64 `json:"token"`
}

type tokenArgs struct {
	Token uint64 `json:"token"`
}

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
