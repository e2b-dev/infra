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

func hasEvent(revents, event int16) bool {
	return revents&event != 0
}

type Userfaultfd struct {
	fd Fd

	src         block.Slicer
	ma          *memory.Mapping
	pageSize    uintptr
	pageTracker *block.Tracker

	// settleRequests guards the pageTracker / prefetchTracker. Workers take
	// RLock for the lookup→install→SetRange sequence; the REMOVE batch takes
	// Lock so a concurrent worker can't overwrite a removed state.
	settleRequests  sync.RWMutex

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

	logger logger.Logger
}

// faultPhase identifies the worker fault hook call site (test-only).
type faultPhase uint8

const (
	faultPhaseBeforeRLock faultPhase = iota
	faultPhaseBeforeFaultPage
)

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(fd uintptr, src block.Slicer, m *memory.Mapping, logger logger.Logger) (*Userfaultfd, error) {
	blockSize := src.BlockSize()

	for _, region := range m.Regions {
		if region.PageSize != uintptr(blockSize) {
			return nil, fmt.Errorf("block size mismatch: %d != %d for region %d", region.PageSize, blockSize, region.BaseHostVirtAddr)
		}
	}

	var wakeupPipe [2]int
	if err := syscall.Pipe2(wakeupPipe[:], syscall.O_NONBLOCK|syscall.O_CLOEXEC); err != nil {
		return nil, fmt.Errorf("failed to create wakeup pipe: %w", err)
	}

	u := &Userfaultfd{
		fd:              Fd(fd),
		src:             src,
		pageSize:        uintptr(blockSize),
		pageTracker:     block.NewTracker(),
		prefetchTracker: block.NewPrefetchTracker(blockSize),
		ma:              m,
		wakeupPipe:      wakeupPipe,
		logger:          logger,
	}

	u.wg.SetLimit(maxRequestsInProgress)

	return u, nil
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

		if hasEvent(pollFds[2].Revents, unix.POLLIN) {
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
				endIdx := startIdx + uint32(rm.end-rm.start)/uint32(u.pageSize)
				u.pageTracker.SetRange(startIdx, endIdx, block.Zero)
				}
				u.settleRequests.Unlock()
			}

			u.readSerial.Unlock()
		} else {
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
				if h := u.testFaultHook.Load(); h != nil {
					(*h)(addr, faultPhaseBeforeRLock)
				}

				// RLock spans the lookup→faultPage→setState sequence so a
				// concurrent REMOVE batch (settleRequests.Lock) can't slip in
				// between the state read and the install.
				u.settleRequests.RLock()
				defer u.settleRequests.RUnlock()

				var source block.Slicer

				idx := uint32(header.BlockIdx(offset, int64(u.pageSize)))

				switch state := u.pageTracker.Get(idx); state {
				case block.Dirty:
					// Pages must not be swappable for this short-circuit to hold:
					// only UFFD_EVENT_REMOVE moves a page out of Dirty.
					return nil
				case block.Zero:
					// Zero-fill. We still owe the kernel an ack for the original
					// MISSING fault or the faulting thread stays blocked.
				case block.NotPresent:
					source = u.src
				default:
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

				handled, err := u.faultPage(
					ctx,
					addr,
					offset,
					accessType,
					source,
					fdExit.SignalExit,
				)
				if err != nil {
					return err
				}

				if handled {
					u.pageTracker.SetRange(idx, idx+1, block.Dirty)
					u.prefetchTracker.Add(offset, accessType)
				} else {
					deferred.push(pf)
					u.signalWakeup()
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
	source block.Slicer,
	onFailure func() error,
) (handled bool, err error) {
	span := trace.SpanFromContext(ctx)

	// Named returns so a recovered panic produces a fatal error: the bare
	// zero values (false, nil) would otherwise be treated as "defer & retry"
	// and a deterministic panic would loop forever.
	defer func() {
		if r := recover(); r != nil {
			u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", u.pageSize), zap.Any("panic", r))
			handled = false
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
			break
		}
		writeErr = u.fd.wake(addr, u.pageSize)
	case source == nil && u.pageSize == header.PageSize && accessType == block.Write:
		writeErr = u.fd.zero(addr, u.pageSize, 0)
	case source == nil && u.pageSize == header.HugepageSize:
		writeErr = u.fd.copy(addr, u.pageSize, header.EmptyHugePage, mode)
	default:
		// Slice retry holds settleRequests.RLock for up to ~2s of
		// exponential backoff, blocking any concurrent REMOVE batch.
		// Correctness holds (uffd FIFO drains the queued REMOVE before
		// the next same-page fault); if the blocking latency ever shows
		// up, move Slice outside the lock and re-check state before
		// UFFDIO_COPY.
		var b []byte
		var dataErr error
		var attempt int

	retryLoop:
		for attempt = range sliceMaxRetries + 1 {
			b, dataErr = source.Slice(ctx, offset, int64(u.pageSize))
			if dataErr == nil {
				break
			}

			if attempt >= sliceMaxRetries || ctx.Err() != nil {
				break
			}

			u.logger.Warn(ctx, "UFFD serve slice error, retrying",
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

			return false, fmt.Errorf("failed to read from source after %d attempts: %w", attempt+1, joinedErr)
		}
		writeErr = u.fd.copy(addr, u.pageSize, b, mode)
	}

	// EEXIST: page already mapped. Wake in case the install used DONTWAKE.
	if errors.Is(writeErr, unix.EEXIST) {
		span.SetAttributes(attribute.Bool("uffd.already_mapped", true))

		u.fd.wake(addr, u.pageSize) //nolint:errcheck // best-effort; thread may already be awake

		return true, nil
	}

	// ESRCH: faulting thread exited during sandbox teardown.
	if errors.Is(writeErr, unix.ESRCH) {
		span.SetAttributes(attribute.Bool("uffd.process_exited", true))
		u.logger.Debug(ctx, "UFFD serve copy error: process no longer exists", zap.Error(writeErr))

		return true, nil
	}

	// EAGAIN: mmap_changing set mid-copy (concurrent madvise/mremap/fork) or
	// a partial copy (see classifyCopyResult). We defer the fault ourselves
	// via deferred.push(pf) + signalWakeup below; the kernel does not
	// auto-redeliver.
	if errors.Is(writeErr, unix.EAGAIN) {
		span.SetAttributes(attribute.Bool("uffd.copy_eagain", true))
		u.logger.Debug(ctx, "UFFD page write EAGAIN, deferring", zap.Uintptr("addr", addr))

		return false, nil
	}

	if writeErr != nil {
		joinedErr := errors.Join(writeErr, safeInvoke(onFailure))

		span.RecordError(joinedErr)
		u.logger.Error(ctx, "UFFD serve uffdio copy error", zap.Error(joinedErr))

		return false, fmt.Errorf("failed uffdio copy: %w", joinedErr)
	}

	return true, nil
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

func (u *Userfaultfd) Close() error {
	syscall.Close(u.wakeupPipe[0])
	syscall.Close(u.wakeupPipe[1])

	return u.fd.close()
}
