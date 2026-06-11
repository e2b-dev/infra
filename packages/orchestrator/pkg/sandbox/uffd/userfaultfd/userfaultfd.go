//go:build linux

package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd")

const maxRequestsInProgress = 4096

const (
	// sliceMaxRetries is the number of times to retry source.Slice() after the initial attempt.
	// Total attempts = sliceMaxRetries + 1.
	sliceMaxRetries = 3
	// sliceRetryBaseDelay is the initial backoff delay before the first retry.
	// Subsequent retries double the delay (exponential backoff), capped at sliceRetryMaxDelay.
	sliceRetryBaseDelay = 50 * time.Millisecond
	// sliceRetryMaxDelay is the maximum backoff delay between retries.
	sliceRetryMaxDelay = 500 * time.Millisecond
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

// ErrClosed reports that the userfaultfd was already closed (sandbox
// teardown) and the requested operation was skipped.
var ErrClosed = errors.New("userfaultfd is closed")

func hasEvent(revents, event int16) bool {
	return revents&event != 0
}

// PageReader is the data source UFFD pulls page contents from on a fault.
type PageReader interface {
	ReadAt(ctx context.Context, p []byte, off int64) (int, error)
}

// pagePool returns HugepageSize-sized scratch buffers shared across all UFFD
// instances. 4 KiB-page UFFDs allocate per fault instead.
var pagePool = sync.Pool{
	New: func() any {
		buf := make([]byte, header.HugepageSize)

		return &buf
	},
}

type Userfaultfd struct {
	fd Fd

	src         PageReader
	ma          *memory.Mapping
	pageSize    uintptr
	pageTracker *block.Tracker

	// settleRequests guards the pageTracker / prefetchTracker. Workers take
	// RLock for the lookup→install→SetRange sequence; the REMOVE batch takes
	// Lock so a concurrent worker can't overwrite a removed state.
	settleRequests sync.RWMutex

	// readSerial serializes serve-loop iterations (read+apply) with snapshot-time
	// Export. Workers do NOT touch this lock — it must remain disjoint from
	// settleRequests so readEvents can always drain the kernel UFFD queue and
	// unblock madvise even when settleRequests.RLock is held by an in-flight
	// worker. See TestNoMadviseDeadlockWithInflightCopy.
	readSerial sync.Mutex

	prefetchTracker *block.PrefetchTracker

	// defaultCopyMode overrides UFFDIO_COPY mode for all faults when non-zero.
	defaultCopyMode CULong

	wg errgroup.Group

	// wakeupPipe is a self-pipe that wakes the poll loop after a worker
	// defers a fault, so a deferred fault isn't orphaned waiting for the
	// next unrelated UFFD event.
	wakeupPipe [2]int

	// testFaultHook is set only by SetTestFaultHook in test builds.
	testFaultHook atomic.Pointer[func(uintptr, faultPhase)]

	// closed is set by Close() under settleRequests.Lock(). Prefault() checks
	// it under settleRequests.RLock() so it never calls UFFDIO_COPY on a fd
	// that has already been closed (and potentially recycled by the OS).
	closed bool

	// Cumulative demand-fault serve counters, read via ServeStats(). They
	// mirror the orchestrator.sandbox.uffd.serve metric but as a per-handler
	// snapshot, so a caller can sample "how many pages did this guest need so
	// far" at a point in time (e.g. the moment envd init returns). Prefaults
	// bypass the serve loop and are not counted here. See recordServeStats.
	servedPages       atomic.Int64 // faults resolved (installed or already-present)
	servedSourcePages atomic.Int64 // subset installed from the source (page_class=new)
	servedBytes       atomic.Int64 // bytes installed into the guest (new + zero)

	logger logger.Logger
}

// faultPhase identifies the worker fault hook call site (test-only).
type faultPhase uint8

const (
	faultPhaseBeforeRLock faultPhase = iota
	faultPhaseBeforeFaultPage
	// faultPhaseBeforePrefaultRLock fires inside Prefault(), before acquiring
	// settleRequests.RLock. Used by TestPrefaultConcurrentWithClose to park
	// Prefault here, call Close() concurrently, and verify the closed flag is
	// checked after the RLock is acquired.
	faultPhaseBeforePrefaultRLock
)

// faultOutcome is the terminal classification of a faultPage call.
type faultOutcome uint8

const (
	// faultInstalled: page was installed by this call.
	faultInstalled faultOutcome = iota
	// faultAlreadyPresent: no install by this call — the page was already
	// mapped (EEXIST), e.g. a concurrent worker or a prefault won the race.
	faultAlreadyPresent
	// faultDeferred: soft failure (EAGAIN); the caller must retry later.
	faultDeferred
	// faultDiscarded: no install happened and retry is pointless
	// (e.g. ESRCH — the faulting thread is gone).
	faultDiscarded
)

// NewUserfaultfdFromFd creates a new userfaultfd instance. Page size is
// taken from the FC-registered regions; all regions must agree.
func NewUserfaultfdFromFd(fd uintptr, src PageReader, m *memory.Mapping, logger logger.Logger) (*Userfaultfd, error) {
	if len(m.Regions) == 0 {
		return nil, errors.New("memory mapping has no regions")
	}
	pageSize := m.Regions[0].PageSize
	if pageSize != header.PageSize && pageSize != header.HugepageSize {
		return nil, fmt.Errorf("unsupported page size: %d", pageSize)
	}
	for _, r := range m.Regions[1:] {
		if r.PageSize != pageSize {
			return nil, fmt.Errorf("region page size mismatch: %d != %d for region %d", r.PageSize, pageSize, r.BaseHostVirtAddr)
		}
	}

	var wakeupPipe [2]int
	if err := syscall.Pipe2(wakeupPipe[:], syscall.O_NONBLOCK|syscall.O_CLOEXEC); err != nil {
		return nil, fmt.Errorf("failed to create wakeup pipe: %w", err)
	}

	u := &Userfaultfd{
		fd:              Fd(fd),
		src:             src,
		pageSize:        pageSize,
		pageTracker:     block.NewTracker(),
		prefetchTracker: block.NewPrefetchTracker(int64(pageSize)),
		ma:              m,
		wakeupPipe:      wakeupPipe,
		logger:          logger,
	}
	u.wg.SetLimit(maxRequestsInProgress)

	return u, nil
}

// ExportPageStates returns snapshots of the faulted and removed page-index
// bitmaps after draining in-flight serve-loop iterations and workers.
// Lock order matches the serve loop to avoid AB-BA inversion.
func (u *Userfaultfd) ExportPageStates() (faulted, removed *roaring.Bitmap) {
	u.readSerial.Lock()
	defer u.readSerial.Unlock()

	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

	return u.pageTracker.Export()
}

func (u *Userfaultfd) readEvents(ctx context.Context) ([]*UffdRemove, []*UffdPagefault, error) {
	buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

	var removes []*UffdRemove
	var pagefaults []*UffdPagefault

	for {
		n, err := syscall.Read(int(u.fd), buf)
		if errors.Is(err, syscall.EINTR) {
			u.logger.Debug(ctx, "uffd: interrupted read. Reading again")

			continue
		}

		if errors.Is(err, syscall.EAGAIN) {
			break
		}

		if err != nil {
			return nil, nil, fmt.Errorf("failed reading uffd: %w", err)
		}

		if n == 0 {
			break
		}

		msg := (*UffdMsg)(unsafe.Pointer(&buf[0]))

		event := getMsgEvent(msg)
		arg := getMsgArg(msg)

		switch event {
		case UFFD_EVENT_PAGEFAULT:
			v := *(*UffdPagefault)(unsafe.Pointer(&arg[0]))
			pagefaults = append(pagefaults, &v)
		case UFFD_EVENT_REMOVE:
			v := *(*UffdRemove)(unsafe.Pointer(&arg[0]))
			removes = append(removes, &v)
		default:
			return nil, nil, ErrUnexpectedEventType
		}
	}

	return removes, pagefaults, nil
}

func (u *Userfaultfd) Serve(
	ctx context.Context,
	fdExit *fdexit.FdExit,
) error {
	// Workers spawned via u.wg.Go may still be running when an error path
	// returns early. Drain them before returning so a worker can't outlive
	// Serve and race with Close() on wakeupPipe / the uffd fd.
	defer func() { _ = u.wg.Wait() }()

	pollFds := []unix.PollFd{
		{Fd: int32(u.fd), Events: unix.POLLIN},
		{Fd: fdExit.Reader(), Events: unix.POLLIN},
		{Fd: int32(u.wakeupPipe[0]), Events: unix.POLLIN},
	}

	emptyDrainCounter := newCounterReporter(u.logger, "uffd: empty drain (spurious wakeup) (accumulated)")
	defer emptyDrainCounter.Close(ctx)

	noDataCounter := newCounterReporter(u.logger, "uffd: no data in fd (accumulated)")
	defer noDataCounter.Close(ctx)

	exitFdErrorCounter := newCounterReporter(u.logger, "uffd: exit fd poll errors (accumulated)")
	defer exitFdErrorCounter.Close(ctx)

	uffdErrorCounter := newCounterReporter(u.logger, "uffd: uffd fd poll errors (accumulated)")
	defer uffdErrorCounter.Close(ctx)

	pollErrorEvents := map[int16]string{
		unix.POLLHUP:  "POLLHUP",
		unix.POLLERR:  "POLLERR",
		unix.POLLNVAL: "POLLNVAL",
	}

	var deferred deferredFaults

	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				u.logger.Debug(ctx, "uffd: interrupted polling, going back to polling")

				continue
			}

			if err == unix.EAGAIN {
				u.logger.Debug(ctx, "uffd: eagain during polling, going back to polling")

				continue
			}

			u.logger.Error(ctx, "UFFD serve polling error", zap.Error(err))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if hasEvent(exitFd.Revents, unix.POLLIN) {
			errMsg := u.wg.Wait()
			if errMsg != nil {
				u.logger.Warn(ctx, "UFFD fd exit error while waiting for goroutines to finish", zap.Error(errMsg))

				return fmt.Errorf("failed to handle uffd: %w", errMsg)
			}

			return nil
		}

		for event, name := range pollErrorEvents {
			if hasEvent(exitFd.Revents, event) {
				exitFdErrorCounter.Increase(name)
			}
		}

		wakeupFired := hasEvent(pollFds[2].Revents, unix.POLLIN)
		if wakeupFired {
			u.drainWakeupPipe()
		}

		uffdFd := pollFds[0]

		for event, name := range pollErrorEvents {
			if hasEvent(uffdFd.Revents, event) {
				uffdErrorCounter.Increase(name)
			}
		}

		var removes []*UffdRemove
		var pagefaults []*UffdPagefault

		if hasEvent(uffdFd.Revents, unix.POLLIN) {
			// readSerial keeps Export from interleaving between read and
			// SetRange(Zero) in the same serve-loop iteration. It does
			// NOT couple readEvents to settleRequests — workers don't take
			// readSerial, so a worker holding settleRequests.RLock can
			// never block this read. See TestNoMadviseDeadlockWithInflightCopy.
			u.readSerial.Lock()

			var err error
			removes, pagefaults, err = u.readEvents(ctx)
			if err != nil {
				u.readSerial.Unlock()
				u.logger.Error(ctx, "uffd: read error", zap.Error(err))

				return fmt.Errorf("failed to read: %w", err)
			}

			if len(removes) > 0 {
				u.settleRequests.Lock()
				for _, rm := range removes {
					// rm.start (inclusive) and rm.end (exclusive) are page-aligned
					// to u.pageSize for the registered VMA (UFFD invariant), so
					// startOff is a multiple of pageSize and length is an integer
					// number of pages — both divisions below are exact, and
					// SetRange's half-open [startIdx, endIdx) lines up with the
					// half-open [rm.start, rm.end).
					startOff, err := u.ma.GetOffset(uintptr(rm.start))
					if err != nil {
						u.logger.Error(ctx, "UFFD REMOVE: failed to map start address",
							zap.Uintptr("start", uintptr(rm.start)), zap.Error(err))

						continue
					}

					startIdx := uint32(header.BlockIdx(startOff, int64(u.pageSize)))
					endIdx := startIdx + uint32(uint64(rm.end-rm.start)/uint64(u.pageSize))
					u.pageTracker.SetRange(startIdx, endIdx, block.Zero)
				}
				u.settleRequests.Unlock()
			}

			u.readSerial.Unlock()
		} else if !wakeupFired {
			// Only treat as "no data" when poll returned without any known
			// source — a wakeupPipe self-wake (worker deferred) is expected.
			noDataCounter.Increase("POLLIN")
		}

		pagefaults = append(deferred.drain(), pagefaults...)

		if len(pagefaults) == 0 {
			if len(removes) == 0 {
				emptyDrainCounter.Increase("EMPTY_DRAIN")
			}

			continue
		}

		emptyDrainCounter.Log(ctx)
		noDataCounter.Log(ctx)

		for _, pf := range pagefaults {
			if pf.flags&UFFD_PAGEFAULT_FLAG_MINOR != 0 {
				return errors.New("unexpected MINOR pagefault event, closing UFFD")
			}

			// WP faults are not registered: we use UFFD_FEATURE_WP_ASYNC.
			if pf.flags&UFFD_PAGEFAULT_FLAG_WP != 0 {
				return errors.New("unexpected WP pagefault event, closing UFFD")
			}

			addr := getPagefaultAddress(pf)
			offset, err := u.ma.GetOffset(addr)
			if err != nil {
				u.logger.Error(ctx, "UFFD serve got mapping error", zap.Error(err))

				return fmt.Errorf("failed to map: %w", err)
			}

			u.wg.Go(func() error {
				// Record serve latency / installed bytes / fault count once
				// per serve attempt, tagged by page class and outcome. Begun
				// before the RLock so the latency covers lock wait + faultPage
				// for this attempt. Note: a deferred fault is re-served (and
				// re-timed) by a later worker, so the guest's full stall —
				// including the deferred-queue wait — can exceed any single
				// recorded serve.
				sw := serveTimer.Begin()
				pclass := pageClassUnknown
				result := faultResultInstalled
				var servedBytes int64
				defer func() {
					sw.RecordRaw(ctx, servedBytes, serveAttrs[pclass][result])
					u.recordServeStats(pclass, result, servedBytes)
				}()

				if h := u.testFaultHook.Load(); h != nil {
					(*h)(addr, faultPhaseBeforeRLock)
				}

				// RLock spans the lookup→faultPage→setState sequence so a
				// concurrent REMOVE batch (settleRequests.Lock) can't slip in
				// between the state read and the install.
				u.settleRequests.RLock()
				defer u.settleRequests.RUnlock()

				var source PageReader

				idx := uint32(header.BlockIdx(offset, int64(u.pageSize)))

				switch state := u.pageTracker.Get(idx); state {
				case block.Dirty:
					// Pages must not be swappable for this short-circuit to hold:
					// only UFFD_EVENT_REMOVE moves a page out of Dirty.
					pclass = pageClassResident
					result = faultResultPresent

					return nil
				case block.Zero:
					// Zero-fill. We still owe the kernel an ack for the original
					// MISSING fault or the faulting thread stays blocked.
					pclass = pageClassZero
				case block.NotPresent:
					pclass = pageClassNew
					source = u.src
				default:
					result = faultResultError

					return fmt.Errorf("unexpected block.State: %#v", state)
				}

				var accessType block.AccessType
				if pf.flags&UFFD_PAGEFAULT_FLAG_WRITE == 0 {
					accessType = block.Read
				} else {
					accessType = block.Write
				}

				if h := u.testFaultHook.Load(); h != nil {
					(*h)(addr, faultPhaseBeforeFaultPage)
				}

				outcome, err := u.faultPage(
					ctx,
					addr,
					offset,
					accessType,
					source,
					fdExit.SignalExit,
				)
				if err != nil {
					result = faultResultError

					return err
				}

				switch outcome {
				case faultInstalled, faultAlreadyPresent:
					if outcome == faultInstalled {
						// A page was installed (zero-filled or pulled from
						// source) by this serve, so count its bytes. On
						// faultAlreadyPresent a concurrent worker or prefault
						// copied the page — record it as "present" with no
						// bytes so the bytes counter stays attributable.
						servedBytes = int64(u.pageSize)
					} else {
						result = faultResultPresent
					}
					// Zero-fill on a read fault installs zero+WP; the page still
					// reads as zero, so keep the tracker entry as Zero so the
					// snapshot diff marks it Empty. WP-async will catch any
					// later write and surface it via DirtyMemory.
					if source != nil || accessType == block.Write {
						u.pageTracker.SetRange(idx, idx+1, block.Dirty)
					}
					u.prefetchTracker.Add(offset, accessType)
				case faultDeferred:
					result = faultResultDeferred
					deferred.push(pf)
					u.signalWakeup()
				case faultDiscarded:
					// No install happened (ESRCH); retry would be pointless.
					result = faultResultDiscarded
				default:
					result = faultResultError

					return fmt.Errorf("unexpected faultOutcome: %#v", outcome)
				}

				return nil
			})
		}
	}
}

func (u *Userfaultfd) faultPage(
	ctx context.Context,
	addr uintptr,
	offset int64,
	accessType block.AccessType,
	source PageReader,
	onFailure func() error,
) (outcome faultOutcome, err error) {
	span := trace.SpanFromContext(ctx)

	// Named returns so a recovered panic produces a fatal error: the bare
	// zero values would otherwise look like a successful install
	// (faultInstalled) and a deterministic panic would loop forever.
	defer func() {
		if r := recover(); r != nil {
			u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", u.pageSize), zap.Any("panic", r))
			outcome = faultDiscarded
			err = fmt.Errorf("uffd serve panic: %v", r)
		}
	}()

	var writeErr error

	mode := u.defaultCopyMode
	if accessType == block.Read {
		mode = UFFDIO_COPY_MODE_WP
	}

	// nil source = zero-fill. 4K read needs zero → WP → wake (anonymous mappings
	// cannot be write-protected until they are populated, so wake must come last).
	switch {
	case source == nil && u.pageSize == header.PageSize && accessType == block.Read:
		writeErr = u.fd.zero(addr, u.pageSize, UFFDIO_ZEROPAGE_MODE_DONTWAKE)
		if writeErr != nil {
			break
		}
		writeErr = u.fd.writeProtect(addr, u.pageSize, UFFDIO_WRITEPROTECT_MODE_WP)
		if writeErr != nil {
			writeErr = errors.Join(writeErr, u.fd.wake(addr, u.pageSize))

			break
		}
		writeErr = u.fd.wake(addr, u.pageSize)
	case source == nil && u.pageSize == header.PageSize && accessType == block.Write:
		writeErr = u.fd.zero(addr, u.pageSize, 0)
	case source == nil && u.pageSize == header.HugepageSize:
		writeErr = u.fd.copy(addr, u.pageSize, header.EmptyHugePage, mode)
	default:
		var b []byte
		if u.pageSize == header.HugepageSize {
			bufPtr := pagePool.Get().(*[]byte)
			defer pagePool.Put(bufPtr)
			b = (*bufPtr)[:u.pageSize]
		} else {
			b = make([]byte, u.pageSize)
		}

		// ReadAt retry holds settleRequests.RLock for up to ~2s of
		// exponential backoff, blocking any concurrent REMOVE batch.
		// Correctness holds (uffd FIFO drains the queued REMOVE before
		// the next same-page fault); if the blocking latency ever shows
		// up, move ReadAt outside the lock and re-check state before
		// UFFDIO_COPY.
		var dataErr error
		var attempt int

	retryLoop:
		for attempt = range sliceMaxRetries + 1 {
			var n int
			n, dataErr = source.ReadAt(ctx, b, offset)
			if dataErr == nil && int64(n) != int64(u.pageSize) {
				dataErr = fmt.Errorf("short read at %d: got %d, want %d", offset, n, u.pageSize)
			}
			if dataErr == nil {
				break
			}

			if attempt >= sliceMaxRetries || ctx.Err() != nil {
				break
			}

			u.logger.Warn(ctx, "UFFD serve read error, retrying",
				zap.Int("attempt", attempt+1),
				zap.Int("max_attempts", sliceMaxRetries+1),
				zap.Error(dataErr),
			)

			delay := min(sliceRetryBaseDelay<<attempt, sliceRetryMaxDelay)
			jitter := time.Duration(rand.Int63n(int64(delay) / 2))

			backoff := time.NewTimer(delay + jitter)

			select {
			case <-ctx.Done():
				backoff.Stop()

				dataErr = errors.Join(dataErr, ctx.Err())

				break retryLoop
			case <-backoff.C:
			}
		}

		if dataErr != nil {
			var signalErr error
			if onFailure != nil {
				signalErr = onFailure()
			}

			joinedErr := errors.Join(dataErr, signalErr)

			span.RecordError(joinedErr)
			u.logger.Error(ctx, "UFFD serve data fetch error after retries",
				zap.Int("attempts", attempt+1),
				zap.Error(joinedErr),
			)

			return faultDiscarded, fmt.Errorf("failed to read from source after %d attempts: %w", attempt+1, joinedErr)
		}
		writeErr = u.fd.copy(addr, u.pageSize, b, mode)
	}

	// EEXIST: page already mapped. Wake in case the install used DONTWAKE.
	if errors.Is(writeErr, unix.EEXIST) {
		span.SetAttributes(attribute.Bool("uffd.already_mapped", true))

		u.fd.wake(addr, u.pageSize) //nolint:errcheck // best-effort; thread may already be awake

		return faultAlreadyPresent, nil
	}

	// ESRCH: faulting thread exited during sandbox teardown. No install
	// happened, but retrying is pointless — the mm is going away.
	if errors.Is(writeErr, unix.ESRCH) {
		span.SetAttributes(attribute.Bool("uffd.process_exited", true))
		u.logger.Debug(ctx, "UFFD serve copy error: process no longer exists", zap.Error(writeErr))

		return faultDiscarded, nil
	}

	// EAGAIN: mmap_changing set mid-copy (concurrent madvise/mremap/fork) or
	// a partial copy (see classifyCopyResult). We defer the fault ourselves
	// via deferred.push(pf) + signalWakeup below; the kernel does not
	// auto-redeliver.
	if errors.Is(writeErr, unix.EAGAIN) {
		span.SetAttributes(attribute.Bool("uffd.copy_eagain", true))
		u.logger.Debug(ctx, "UFFD page write EAGAIN, deferring", zap.Uintptr("addr", addr))

		return faultDeferred, nil
	}

	if writeErr != nil {
		joinedErr := errors.Join(writeErr, safeInvoke(onFailure))

		span.RecordError(joinedErr)
		u.logger.Error(ctx, "UFFD serve uffdio copy error", zap.Error(joinedErr))

		return faultDiscarded, fmt.Errorf("failed uffdio copy: %w", joinedErr)
	}

	return faultInstalled, nil
}

func (u *Userfaultfd) PrefetchData() block.PrefetchData {
	// Hold Lock across the read — Lock; Unlock; Read leaves a window
	// where a worker can RLock and mutate prefetchTracker before we read.
	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

	return u.prefetchTracker.PrefetchData()
}

func (u *Userfaultfd) signalWakeup() {
	syscall.Write(u.wakeupPipe[1], []byte{1}) //nolint:errcheck // best-effort; pipe is non-blocking
}

func (u *Userfaultfd) drainWakeupPipe() {
	var buf [64]byte
	for {
		_, err := syscall.Read(u.wakeupPipe[0], buf[:])
		if err != nil {
			break
		}
	}
}

// PageSize returns the FC region page size driving this UFFD.
func (u *Userfaultfd) PageSize() int64 {
	return int64(u.pageSize)
}

func (u *Userfaultfd) Close() error {
	// Hold the write lock for the entire close sequence so that:
	//   (a) any Prefault() caller currently holding RLock finishes its
	//       UFFDIO_COPY before the fd number is freed and potentially
	//       recycled by the OS;
	//   (b) Close() is idempotent — a second call sees closed==true and
	//       returns immediately without touching already-freed fds.
	// In production Serve() drains all workers (u.wg.Wait) before
	// returning, so this Lock() is always uncontended when Close() fires.
	u.settleRequests.Lock()
	defer u.settleRequests.Unlock()

	if u.closed {
		return nil
	}
	u.closed = true

	syscall.Close(u.wakeupPipe[0])
	syscall.Close(u.wakeupPipe[1])

	return u.fd.close()
}
