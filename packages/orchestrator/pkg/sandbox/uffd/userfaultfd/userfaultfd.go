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

	// We use the settleRequests to guard the pageTracker so we can access a consistent state of the pageTracker after the requests are finished.
	settleRequests sync.RWMutex

	prefetchTracker *block.PrefetchTracker

	wg errgroup.Group

	// defaultCopyMode overrides the UFFDIO_COPY mode for all faults when non-zero.
	defaultCopyMode CULong

	// installed only by SetTestFaultHook in test builds; nil in production.
	testFaultHook atomic.Pointer[func(uintptr, faultPhase)]

	logger logger.Logger
}

// faultPhase identifies WHEN inside the worker the (test-only) fault hook is
// invoked. Production builds never install a hook (testFaultHook is nil); the
// per-fault overhead is then a single atomic load + nil check per call site.
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

	u := &Userfaultfd{
		fd:              Fd(fd),
		src:             src,
		pageSize:        uintptr(blockSize),
		pageTracker:     newPageTracker(uintptr(blockSize)),
		prefetchTracker: block.NewPrefetchTracker(blockSize),
		ma:              m,
		logger:          logger,
	}

	// By default this was unlimited.
	// Now that we don't skip previously faulted pages we add at least some boundaries to the concurrency.
	// Also, in some brief tests, adding a limit actually improved the handling at high concurrency.
	u.wg.SetLimit(maxRequestsInProgress)

	return u, nil
}

func (u *Userfaultfd) Close() error {
	return u.fd.close()
}

func (u *Userfaultfd) Serve(
	ctx context.Context,
	fdExit *fdexit.FdExit,
) error {
	pollFds := []unix.PollFd{
		{Fd: int32(u.fd), Events: unix.POLLIN},
		{Fd: fdExit.Reader(), Events: unix.POLLIN},
	}

	eagainCounter := newCounterReporter(u.logger, "uffd: eagain with no pagefaults (accumulated)")
	defer eagainCounter.Close(ctx)

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

		uffdFd := pollFds[0]

		// Track uffd error events
		for event, name := range pollErrorEvents {
			if hasEvent(uffdFd.Revents, event) {
				uffdErrorCounter.Increase(name)
			}
		}

		if !hasEvent(uffdFd.Revents, unix.POLLIN) {
			// Uffd is not ready for reading as there is nothing to read on the fd.
			// https://github.com/firecracker-microvm/firecracker/issues/5056
			// https://elixir.bootlin.com/linux/v6.8.12/source/fs/userfaultfd.c#L1149
			// TODO: Check for all the errors
			// - https://docs.kernel.org/admin-guide/mm/userfaultfd.html
			// - https://elixir.bootlin.com/linux/v6.8.12/source/fs/userfaultfd.c
			// - https://man7.org/linux/man-pages/man2/userfaultfd.2.html
			// It might be possible to just check for data != 0 in the syscall.Read loop
			// but I don't feel confident about doing that.
			noDataCounter.Increase("POLLIN")

			continue
		}

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		var pagefaults []*UffdPagefault
		for {
			_, err := syscall.Read(int(u.fd), buf)
			if err == syscall.EINTR {
				u.logger.Debug(ctx, "uffd: interrupted read, reading again")

				continue
			}

			if err == syscall.EAGAIN {
				break
			}

			if err != nil {
				u.logger.Error(ctx, "uffd: read error", zap.Error(err))

				return fmt.Errorf("failed to read: %w", err)
			}

			msg := *(*UffdMsg)(unsafe.Pointer(&buf[0]))

			if msgEvent := getMsgEvent(&msg); msgEvent != UFFD_EVENT_PAGEFAULT {
				u.logger.Error(ctx, "UFFD serve unexpected event type", zap.Any("event_type", msgEvent))

				return ErrUnexpectedEventType
			}

			arg := getMsgArg(&msg)
			pagefault := *(*UffdPagefault)(unsafe.Pointer(&arg[0]))
			pagefaults = append(pagefaults, &pagefault)
		}

		if len(pagefaults) == 0 {
			eagainCounter.Increase("EMPTY_DRAIN")

			continue
		}

		eagainCounter.Log(ctx)
		noDataCounter.Log(ctx)

		for _, pagefault := range pagefaults {
			flags := pagefault.flags

			addr := getPagefaultAddress(pagefault)

			offset, err := u.ma.GetOffset(addr)
			if err != nil {
				u.logger.Error(ctx, "UFFD serve get mapping error", zap.Error(err))

				return fmt.Errorf("failed to map: %w", err)
			}

			// Handle write to missing page (WRITE flag)
			// If the event has WRITE flag, it was a write to a missing page.
			// For the write to be executed, we first need to copy the page from the source to the guest memory.
			if flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
				u.wg.Go(func() error {
					if h := u.testFaultHook.Load(); h != nil {
						(*h)(addr, faultPhaseBeforeRLock)
					}

					return u.faultPage(ctx, addr, offset, u.src, fdExit.SignalExit, block.Write)
				})

				continue
			}

			// Handle read to missing page ("MISSING" flag)
			// If the event has no flags, it was a read to a missing page and we need to copy the page from the source to the guest memory.
			if flags == 0 {
				u.wg.Go(func() error {
					if h := u.testFaultHook.Load(); h != nil {
						(*h)(addr, faultPhaseBeforeRLock)
					}

					return u.faultPage(ctx, addr, offset, u.src, fdExit.SignalExit, block.Read)
				})

				continue
			}

			// MINOR and WP flags are not expected as we don't register the uffd with these flags.
			return fmt.Errorf("unexpected event type: %d, closing uffd", flags)
		}
	}
}

func (u *Userfaultfd) PrefetchData() block.PrefetchData {
	// This will be at worst cancelled when the uffd is closed.
	u.settleRequests.Lock()
	// The locking here would work even without using defer (just lock-then-unlock the mutex), but at this point let's make it lock to the clone,
	// so it is consistent even if there is a another uffd call after.
	defer u.settleRequests.Unlock()

	return u.prefetchTracker.PrefetchData()
}

func (u *Userfaultfd) faultPage(
	ctx context.Context,
	addr uintptr,
	offset int64,
	source block.Slicer,
	onFailure func() error,
	accessType block.AccessType,
) error {
	span := trace.SpanFromContext(ctx)

	// The RLock must be called inside the goroutine to ensure RUnlock runs via defer,
	// even if the errgroup is cancelled or the goroutine returns early.
	// This guards against races between marking the page faulted / prefetched
	// and another caller observing the pageTracker or prefetchTracker.
	u.settleRequests.RLock()
	defer u.settleRequests.RUnlock()

	if h := u.testFaultHook.Load(); h != nil {
		(*h)(addr, faultPhaseBeforeFaultPage)
	}

	defer func() {
		if r := recover(); r != nil {
			u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", u.pageSize), zap.Any("panic", r))
		}
	}()

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
		signalErr := safeInvoke(onFailure)

		joinedErr := errors.Join(dataErr, signalErr)

		span.RecordError(joinedErr)
		u.logger.Error(ctx, "UFFD serve data fetch error after retries",
			zap.Int("attempts", attempt+1),
			zap.Error(joinedErr),
		)

		return fmt.Errorf("failed to read from source after %d attempts: %w", attempt+1, joinedErr)
	}

	copyMode := u.defaultCopyMode

	// Performing copy() on UFFD clears the WP bit unless we explicitly tell
	// it not to. We do that for faults caused by a read access. Write accesses
	// would anyways cause clear the write-protection bit.
	if accessType != block.Write {
		copyMode |= UFFDIO_COPY_MODE_WP
	}

	copyErr := u.fd.copy(addr, u.pageSize, b, copyMode)
	if errors.Is(copyErr, unix.EEXIST) {
		// Page is already mapped
		span.SetAttributes(attribute.Bool("uffd.already_mapped", true))

		return nil
	}

	if errors.Is(copyErr, unix.ESRCH) {
		// The faulting thread/process no longer exists — it exited or was killed
		// while the page fetch was in flight. This is expected during sandbox
		// teardown; treat it as benign.
		span.SetAttributes(attribute.Bool("uffd.process_exited", true))
		u.logger.Debug(ctx, "UFFD serve copy error: process no longer exists", zap.Error(copyErr))

		return nil
	}

	if copyErr != nil {
		signalErr := safeInvoke(onFailure)

		joinedErr := errors.Join(copyErr, signalErr)

		span.RecordError(joinedErr)
		u.logger.Error(ctx, "UFFD serve uffdio copy error", zap.Error(joinedErr))

		return fmt.Errorf("failed uffdio copy: %w", joinedErr)
	}

	u.pageTracker.setState(addr, addr+u.pageSize, faulted)
	u.prefetchTracker.Add(offset, accessType)

	return nil
}
