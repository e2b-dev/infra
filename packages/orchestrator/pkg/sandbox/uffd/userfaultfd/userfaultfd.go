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

// hasEvent checks if a specific poll event flag is set in revents.
func hasEvent(revents, event int16) bool {
	return revents&event != 0
}

type Userfaultfd struct {
	fd Fd

	src         block.Slicer
	ma          *memory.Mapping
	pageSize    uintptr
	pageTracker *pageTracker

	// settleRequests guards the pageTracker and prefetchTracker so we can access a
	// consistent state after in-flight requests have finished, and so REMOVE events
	// can update the pageTracker without racing with concurrent faultPage workers.
	settleRequests  sync.RWMutex
	prefetchTracker *block.PrefetchTracker

	// defaultCopyMode overrides the UFFDIO_COPY mode for all faults when non-zero.
	defaultCopyMode CULong

	wg errgroup.Group

	// wakeupPipe is a self-pipe used to wake the poll loop when a goroutine
	// defers a page fault. Without this, a deferred fault could be orphaned
	// if no new UFFD events arrive to wake poll.
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
		pageTracker:     newPageTracker(uintptr(blockSize)),
		prefetchTracker: block.NewPrefetchTracker(blockSize),
		ma:              m,
		wakeupPipe:      wakeupPipe,
		logger:          logger,
	}

	// By default this was unlimited.
	// Now that we don't skip previously faulted pages we add at least some boundaries to the concurrency.
	// Also, in some brief tests, adding a limit actually improved the handling at high concurrency.
	u.wg.SetLimit(maxRequestsInProgress)

	return u, nil
}

// readEvents reads all available UFFD events from the file descriptor,
// returning removes and pagefaults separately.
func (u *Userfaultfd) readEvents(ctx context.Context) ([]*UffdRemove, []*UffdPagefault, error) {
	// We are reusing the same buffer for all events, but that's fine,
	// because getMsgArg, will make a copy of the actual event from `buf`
	// and it's a pointer to this copy that we are returning to caller.
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
			// EAGAIN means that we have drained all the available events for the file descriptor.
			// We are done.
			break
		}

		if err != nil {
			return nil, nil, fmt.Errorf("failed reading uffd: %w", err)
		}

		// `Read` returned with 0 bytes actually read. No more events to read
		// and the writing end has been closed. This should never happen, unless
		// something (us or Firecracker) closes the file descriptor
		// TODO: Ignore it for now, but maybe we should return an error(?)
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

		// Track exit fd error events
		for event, name := range pollErrorEvents {
			if hasEvent(exitFd.Revents, event) {
				exitFdErrorCounter.Increase(name)
			}
		}

		// Drain the wakeup pipe if it fired (a goroutine deferred a fault).
		if hasEvent(pollFds[2].Revents, unix.POLLIN) {
			u.drainWakeupPipe()
		}

		uffdFd := pollFds[0]

		// Track uffd error events
		for event, name := range pollErrorEvents {
			if hasEvent(uffdFd.Revents, event) {
				uffdErrorCounter.Increase(name)
			}
		}

		var removes []*UffdRemove
		var pagefaults []*UffdPagefault

		if hasEvent(uffdFd.Revents, unix.POLLIN) {
			var err error
			removes, pagefaults, err = u.readEvents(ctx)
			if err != nil {
				u.logger.Error(ctx, "uffd: read error", zap.Error(err))

				return fmt.Errorf("failed to read: %w", err)
			}
		} else {
			noDataCounter.Increase("POLLIN")
		}

		// First handle the UFFD_EVENT_REMOVE events. Take the settleRequests write lock to ensure that no
		// other page or pre-fault operation is running concurrently.
		// A goroutine from the previous batch or a prefault operation could still be executing
		// setState(faulted) after its UFFDIO_COPY returned. If we process a REMOVE for the same
		// page before that goroutine finishes, the goroutine's setState(faulted) would
		// overwrite the removed state we just set.
		if len(removes) > 0 {
			u.settleRequests.Lock()
			for _, rm := range removes {
				u.pageTracker.setState(uintptr(rm.start), uintptr(rm.end), removed)
			}
			u.settleRequests.Unlock()
		}

		// Collect deferred pagefaults from previous goroutines that got EAGAIN.
		// The wakeup pipe ensures we don't sleep through these.
		pagefaults = append(deferred.drain(), pagefaults...)

		if len(pagefaults) == 0 {
			// No pagefaults to handle: spurious wakeup if no removes either.
			if len(removes) == 0 {
				emptyDrainCounter.Increase("EMPTY_DRAIN")
			}

			continue
		}

		emptyDrainCounter.Log(ctx)
		noDataCounter.Log(ctx)

		for _, pf := range pagefaults {
			// We don't handle minor page faults.
			if pf.flags&UFFD_PAGEFAULT_FLAG_MINOR != 0 {
				return errors.New("unexpected MINOR pagefault event, closing UFFD")
			}

			// We don't handle write-protection page faults, we're using asynchronous write protection.
			if pf.flags&UFFD_PAGEFAULT_FLAG_WP != 0 {
				return errors.New("unexpected WP pagefault event, closing UFFD")
			}

			addr := getPagefaultAddress(pf)
			offset, err := u.ma.GetOffset(addr)
			if err != nil {
				u.logger.Error(ctx, "UFFD serve got mapping error", zap.Error(err))

				return fmt.Errorf("failed to map: %w", err)
			}

			// State read happens before the worker takes settleRequests.RLock,
			// so a REMOVE arriving in the parent's next iteration can race
			// with an already-scheduled worker. Tracked as a known race; fix
			// re-reads state under RLock in the worker.
			var source block.Slicer
			switch state := u.pageTracker.get(addr); state {
			case faulted:
				// Already mapped (prefault or earlier fault in this batch).
				// Only a UFFD_EVENT_REMOVE can transition out of `faulted`;
				// the used pages must not be swappable for this to hold.
				continue
			case removed:
				// Zero-fill: source stays nil.
			case missing:
				source = u.src
			default:
				return fmt.Errorf("unexpected pageState: %#v", state)
			}

			u.wg.Go(func() error {
				if h := u.testFaultHook.Load(); h != nil {
					(*h)(addr, faultPhaseBeforeRLock)
				}

				// RLock inside the goroutine so RUnlock runs via defer even on
				// early return, and so it pairs with the prefetchTracker write
				// below.
				u.settleRequests.RLock()
				defer u.settleRequests.RUnlock()

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
					u.pageTracker.setState(addr, addr+u.pageSize, faulted)
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

	// Named returns so a recovered panic surfaces as a fatal error instead of
	// the zero values (false, nil) — which the caller treats as "defer & retry"
	// and would loop indefinitely on a deterministic panic.
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

	// Write to guest memory. nil data means zero-fill
	switch {
	case source == nil && u.pageSize == header.PageSize && accessType == block.Read:
		// Firecracker uses anonymous mappings for 4K pages. Anonymous mappings can only
		// be write protected once pages are populated. We need to enable write-protection
		// *after* we serve the page fault.
		//
		// To avoid the race condition, first serve the page without waking the thread
		writeErr = u.fd.zero(addr, u.pageSize, UFFDIO_ZEROPAGE_MODE_DONTWAKE)
		if writeErr != nil {
			break
		}
		// Then, write-protect the page
		writeErr = u.fd.writeProtect(addr, u.pageSize, UFFDIO_WRITEPROTECT_MODE_WP)
		if writeErr != nil {
			break
		}
		// And, finally, wake up the faulting thread
		writeErr = u.fd.wake(addr, u.pageSize)
	case source == nil && u.pageSize == header.PageSize && accessType == block.Write:
		// If this was a write access to a 4K page simply provide the zero page (clearing the WP bit)
		// and wake up the thread in one step.
		writeErr = u.fd.zero(addr, u.pageSize, 0)
	case source == nil && u.pageSize == header.HugepageSize:
		writeErr = u.fd.copy(addr, u.pageSize, header.EmptyHugePage, mode)
	default:
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

	// Page is already mapped (e.g. pre-faulted, or a previous DONTWAKE zero succeeded
	// but the subsequent writeProtect/wake step failed with EAGAIN and the fault was deferred).
	// Wake the thread unconditionally: if the thread is sleeping due to DONTWAKE this
	// unblocks it; if it was already woken the wake is a harmless no-op.
	if errors.Is(writeErr, unix.EEXIST) {
		span.SetAttributes(attribute.Bool("uffd.already_mapped", true))

		u.fd.wake(addr, u.pageSize) //nolint:errcheck // best-effort; thread may already be awake

		return true, nil
	}

	if errors.Is(writeErr, unix.ESRCH) {
		// The faulting thread/process no longer exists — it exited or was killed
		// while the page fetch was in flight. This is expected during sandbox
		// teardown; treat it as benign.
		span.SetAttributes(attribute.Bool("uffd.process_exited", true))
		u.logger.Debug(ctx, "UFFD serve copy error: process no longer exists", zap.Error(writeErr))

		return true, nil
	}

	if errors.Is(writeErr, unix.EAGAIN) {
		// mmap_changing was set during the copy (concurrent
		// madvise(MADV_DONTNEED)/mremap/fork on the mm). Drop the fault and
		// let the kernel redeliver. See classifyCopyResult for the
		// partial-copy variant that maps onto this branch.
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
	// This will be at worst cancelled when the uffd is closed.
	u.settleRequests.Lock()
	u.settleRequests.Unlock() //nolint:staticcheck // SA2001: intentional — we just need to settle the read locks.

	return u.prefetchTracker.PrefetchData()
}

// signalWakeup writes a byte to the wakeup pipe to unblock poll.
// Safe to call from any goroutine; spurious writes are harmless.
func (u *Userfaultfd) signalWakeup() {
	syscall.Write(u.wakeupPipe[1], []byte{1}) //nolint:errcheck // best-effort; pipe is non-blocking
}

// drainWakeupPipe consumes all bytes from the wakeup pipe so it doesn't
// keep firing on the next poll.
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
