package userfaultfd

// This test creates the userfaultfd in the parent test process and
// drives it from a child helper process. We do this so the actual
// page-fault handling runs in a process where we can fully control
// memory layout (no Go GC scanning / touching the registered region)
// — which mirrors how Firecracker uses UFFD in production.
//
// All parent ↔ child coordination — readiness, page-state queries,
// pause/resume, fault barriers, shutdown — flows over a single Unix
// domain socket using the standard-library net/rpc + jsonrpc codec.
// Each in-flight RPC runs in its own server-side goroutine, so a
// blocking handler (e.g. WaitFaultHeld) does not stall other RPCs.
// The only fd we still hand off out-of-band is the userfaultfd
// itself (kernel object, has to go through ExtraFiles); the initial
// source data is written to a temp file under t.TempDir() because
// base64-stuffing megabytes through the JSON envelope would be silly.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

// Env vars used by the child helper process.
const (
	envHelperFlag    = "GO_TEST_HELPER_PROCESS"
	envSocketPath    = "GO_UFFD_SOCKET"
	envContentPath   = "GO_UFFD_CONTENT"
	envMmapStart     = "GO_UFFD_MMAP_START"
	envMmapPagesize  = "GO_UFFD_MMAP_PAGESIZE"
	envMmapTotalSize = "GO_UFFD_MMAP_SIZE"
	envAlwaysWP      = "GO_UFFD_ALWAYS_WP"
	envGated         = "GO_UFFD_GATED"
	// envBarriers gates the test-only worker hooks. Only race tests
	// need them; for everyone else we leave the hook fields nil so
	// the hot path stays a single nil-pointer load + branch.
	envBarriers = "GO_UFFD_BARRIERS"
)

// ---- RPC method types ---------------------------------------------------
//
// net/rpc requires methods of the form:
//
//   func (s *Service) Method(args *ArgsT, reply *ReplyT) error
//
// where both args and reply are exported pointer types. For methods
// that take or return nothing meaningful we still need a type — Empty
// fills that role.

type Empty struct{}

type PageStatesReply struct {
	Entries []pageStateEntry
}

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

// pageStateEntry is the wire format for PageStates RPC results.
type pageStateEntry struct {
	State  uint8
	Offset uint64
}

// ---- Parent side --------------------------------------------------------

// childForkMu serialises the cmd.Start() call across all parallel
// cross-process tests in this binary. Without it, the duplicated
// uffd fd we hand to one child via ExtraFiles is briefly visible in
// the parent's fd table while ANOTHER concurrent test calls fork()
// — so that other test's child inherits a uffd fd it does not own.
// The leaked fd keeps the original test's uffd kernel object alive
// after its owner closes its end and produces hard-to-diagnose
// -parallel-only deadlocks.
//
// Holding the mutex only across cmd.Start (which itself holds the
// process lock for the underlying syscall.ForkExec) is enough — by
// the time Start returns the dup'd fd is already mapped into fd 3
// in the new child and we close it immediately in the parent below.
var childForkMu sync.Mutex

// Main process, FC in our case.
func configureCrossProcessTest(ctx context.Context, t *testing.T, tt testConfig) (*testHandler, error) {
	t.Helper()

	data := RandomPages(tt.pagesize, tt.numberOfPages)

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

	tmpDir := t.TempDir()

	contentPath := filepath.Join(tmpDir, "content.bin")
	require.NoError(t, os.WriteFile(contentPath, data.Content(), 0o600))

	socketPath := filepath.Join(tmpDir, "rpc.sock")
	listenCfg := net.ListenConfig{}
	listener, err := listenCfg.Listen(ctx, "unix", socketPath)
	require.NoError(t, err)

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperServingProcess", "-test.timeout=0")
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
	if tt.barriers {
		cmd.Env = append(cmd.Env, envBarriers+"=1")
	}

	// We hand the uffd fd to the child via ExtraFiles. The child-
	// side dup3 inside fork+exec clears CLOEXEC on the destination
	// fd (i.e. fd 3 in the child) automatically, so the SOURCE fd
	// in our parent should remain CLOEXEC — otherwise every other
	// test fork()'d concurrently from this binary inherits a uffd
	// it does not own and hangs surface at higher -parallel.
	childForkMu.Lock()

	dup, err := syscall.Dup(int(uffdFd))
	require.NoError(t, err)
	if _, err := unix.FcntlInt(uintptr(dup), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
		childForkMu.Unlock()
		require.NoError(t, err)
	}

	uffdFile := os.NewFile(uintptr(dup), "uffd")
	cmd.ExtraFiles = []*os.File{uffdFile}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	startErr := cmd.Start()
	uffdFile.Close()
	childForkMu.Unlock()

	require.NoError(t, startErr)

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
	case <-time.After(10 * time.Second):
		listener.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()

		return nil, errors.New("child did not connect within 10s")
	}
	listener.Close()

	client := jsonrpc.NewClient(conn)

	h := &testHandler{
		memoryArea: &memoryArea,
		pagesize:   tt.pagesize,
		data:       data,
		client:     client,
		conn:       conn,
		cmd:        cmd,
	}

	// WaitReady blocks on the child until its initial setup is done
	// (uffd serve goroutine running, hooks installed). The RPC reply
	// IS the readiness signal — no separate ready pipe / signal
	// needed.
	require.NoError(t, h.client.Call("Service.WaitReady", &Empty{}, &Empty{}))

	t.Cleanup(func() {
		// Best-effort graceful shutdown via RPC. If the child has
		// already crashed the RPC will error and we fall back to
		// killing the process below.
		_ = h.client.Call("Service.Shutdown", &Empty{}, &Empty{})
		_ = client.Close()

		waitErr := cmd.Wait()
		if waitErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(waitErr, &exitErr) {
				t.Logf("helper process Wait: %v", waitErr)
			}
		}

		// Tear down the UFFD registration before the early uffdFd.close()
		// cleanup runs. Today this is a no-op (no test enables
		// UFFD_FEATURE_EVENT_REMOVE) but a follow-up that does will
		// otherwise see munmap block on un-acked REMOVE events queued
		// against the still-registered range. Cleanups run LIFO, so
		// this fires before the close registered earlier.
		assert.NoError(t, unregister(uffdFd, memoryStart, uint64(size)))
	})

	if tt.gated {
		h.servePause = func() error {
			return h.client.Call("Service.ServePause", &Empty{}, &Empty{})
		}
		h.serveResume = func() error {
			return h.client.Call("Service.ServeResume", &Empty{}, &Empty{})
		}
	}

	h.pageStatesOnce = func() (handlerPageStates, error) {
		var reply PageStatesReply
		if err := h.client.Call("Service.PageStates", &Empty{}, &reply); err != nil {
			return handlerPageStates{}, err
		}

		var states handlerPageStates
		for _, e := range reply.Entries {
			if pageState(e.State) == faulted {
				states.faulted = append(states.faulted, uint(e.Offset))
			}
		}
		slices.Sort(states.faulted)

		return states, nil
	}

	return h, nil
}

// ---- Child side ---------------------------------------------------------

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
// parent socket, registers the RPC service, and runs uffd.Serve in a
// background goroutine that pause/resume RPCs can stop and restart.
func crossProcessServe() error {
	socketPath := os.Getenv(envSocketPath)
	if socketPath == "" {
		return fmt.Errorf("missing %s", envSocketPath)
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(context.Background(), "unix", socketPath)
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

	br := newBarrierRegistry()

	// Hooks are only wired up when the test asked for them (race
	// tests). For everyone else we leave the bundle nil so the hot
	// path is a single nil-pointer load + branch.
	if os.Getenv(envBarriers) == "1" {
		uffd.SetTestHooks(&testHooks{
			beforeWorkerRLock: br.hookFor(barrierBeforeRLock),
			beforeFaultPage:   br.hookFor(barrierBeforeFaultPage),
		})
	}

	gated := os.Getenv(envGated) == "1"

	svc := &Service{
		uffd:     uffd,
		br:       br,
		gated:    gated,
		shutdown: make(chan struct{}),
	}
	svc.startServe()

	server := rpc.NewServer()
	if err := server.Register(svc); err != nil {
		return fmt.Errorf("rpc Register: %w", err)
	}

	// Run the codec in a goroutine so we can react to Shutdown
	// without depending on the codec returning.
	codecDone := make(chan struct{})
	go func() {
		defer close(codecDone)
		server.ServeCodec(jsonrpc.NewServerCodec(conn))
	}()

	select {
	case <-svc.shutdown:
	case <-codecDone:
	}

	// Release any still-parked barriers so the serve goroutine can
	// finish, then stop the serve goroutine.
	br.releaseAll()
	svc.stopServe()

	// Closing the conn is sufficient to unblock ServeCodec if it
	// hasn't already returned.
	_ = conn.Close()
	<-codecDone

	return nil
}

// Service is the RPC surface exposed to the parent. Methods follow
// net/rpc's required signature.
type Service struct {
	uffd *Userfaultfd
	br   *barrierRegistry

	gated bool

	mu       sync.Mutex
	stop     func() // currently active serve-stop function, nil if paused
	shutdown chan struct{}
	closed   bool
}

// startServe spawns the uffd Serve goroutine and stores its stop fn.
// Caller must hold s.mu. Idempotent: if a serve goroutine is already
// running (s.stop != nil) this is a no-op so a stray duplicate
// ServeResume can't leak an untracked Serve goroutine and break later
// pauses.
func (s *Service) startServe() {
	if s.stop != nil {
		return
	}

	exit, err := fdexit.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fdexit.New:", err)

		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.uffd.Serve(context.Background(), exit); err != nil {
			fmt.Fprintln(os.Stderr, "uffd.Serve:", err)
		}
	}()

	s.stop = func() {
		_ = exit.SignalExit()
		<-done
		exit.Close()
	}
}

func (s *Service) stopServe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
}

// WaitReady is a no-op handler whose successful reply is the
// readiness signal for the parent.
func (s *Service) WaitReady(_ *Empty, _ *Empty) error {
	return nil
}

func (s *Service) PageStates(_ *Empty, reply *PageStatesReply) error {
	entries, err := s.uffd.pageStateEntries()
	if err != nil {
		return err
	}
	reply.Entries = entries

	return nil
}

func (s *Service) ServePause(_ *Empty, _ *Empty) error {
	if !s.gated {
		return errors.New("ServePause called on a non-gated handler")
	}
	s.stopServe()

	return nil
}

func (s *Service) ServeResume(_ *Empty, _ *Empty) error {
	if !s.gated {
		return errors.New("ServeResume called on a non-gated handler")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startServe()

	return nil
}

func (s *Service) InstallFaultBarrier(args *FaultBarrierArgs, reply *FaultBarrierReply) error {
	reply.Token = s.br.install(uintptr(args.Addr), barrierPoint(args.Point))

	return nil
}

func (s *Service) WaitFaultHeld(args *TokenArgs, _ *Empty) error {
	return s.br.waitArrived(context.Background(), args.Token)
}

func (s *Service) ReleaseFault(args *TokenArgs, _ *Empty) error {
	s.br.release(args.Token)

	return nil
}

func (s *Service) Shutdown(_ *Empty, _ *Empty) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.shutdown)
	}

	return nil
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

// ---- Barrier registry ---------------------------------------------------

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
