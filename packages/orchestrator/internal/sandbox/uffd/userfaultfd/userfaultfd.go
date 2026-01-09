package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/trace"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd")

const maxRequestsInProgress = 4096

var ErrUnexpectedEventType = errors.New("unexpected event type")

type Userfaultfd struct {
	fd Fd

	src block.Slicer
	ma  *memory.Mapping

	// We don't skip the already mapped pages, because if the memory is swappable the page *might* under some conditions be mapped out.
	// For hugepages this should not be a problem, but might theoretically happen to normal pages with swap
	missingRequests *block.Tracker
	// We use the settleRequests to guard the missingRequests so we can access a consistent state of the missingRequests after the requests are finished.
	settleRequests sync.RWMutex

	// Page fault tracing - uses shared trace.EventRecorder
	tracer *trace.EventRecorder

	wg errgroup.Group

	logger logger.Logger
}

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
		missingRequests: block.NewTracker(blockSize),
		ma:              m,
		tracer:          trace.NewEventRecorder(false),
		logger:          logger,
	}

	// By default this was unlimited.
	// Now that we don't skip previously faulted pages we add at least some boundaries to the concurrency.
	// Also, in some brief tests, adding a limit actually improved the handling at high concurrency.
	u.wg.SetLimit(maxRequestsInProgress)

	return u, nil
}

// SetTraceEnabled enables or disables page fault tracing.
func (u *Userfaultfd) SetTraceEnabled(enabled bool) {
	u.tracer.SetEnabled(enabled)
}

// GetPageFaultTrace returns a copy of the page fault trace.
func (u *Userfaultfd) GetPageFaultTrace() []trace.Event {
	return u.tracer.Events()
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

	eagainCounter := newEagainCounter(u.logger, "uffd: eagain during fd read (accumulated)")
	defer eagainCounter.Close(ctx)

outerLoop:
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
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := u.wg.Wait()
			if errMsg != nil {
				u.logger.Warn(ctx, "UFFD fd exit error while waiting for goroutines to finish", zap.Error(errMsg))

				return fmt.Errorf("failed to handle uffd: %w", errMsg)
			}

			return nil
		}

		uffdFd := pollFds[0]
		if uffdFd.Revents&unix.POLLIN == 0 {
			// Uffd is not ready for reading as there is nothing to read on the fd.
			// https://github.com/firecracker-microvm/firecracker/issues/5056
			// https://elixir.bootlin.com/linux/v6.8.12/source/fs/userfaultfd.c#L1149
			// TODO: Check for all the errors
			// - https://docs.kernel.org/admin-guide/mm/userfaultfd.html
			// - https://elixir.bootlin.com/linux/v6.8.12/source/fs/userfaultfd.c
			// - https://man7.org/linux/man-pages/man2/userfaultfd.2.html
			// It might be possible to just check for data != 0 in the syscall.Read loop
			// but I don't feel confident about doing that.
			u.logger.Debug(ctx, "uffd: no data in fd, going back to polling")

			continue
		}

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		for {
			_, err := syscall.Read(int(u.fd), buf)
			if err == syscall.EINTR {
				u.logger.Debug(ctx, "uffd: interrupted read, reading again")

				continue
			}

			if err == nil {
				// There is no error so we can proceed.

				eagainCounter.Log(ctx)

				break
			}

			if err == syscall.EAGAIN {
				eagainCounter.Increase()

				// Continue polling the fd.
				continue outerLoop
			}

			u.logger.Error(ctx, "uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := *(*UffdMsg)(unsafe.Pointer(&buf[0]))

		if msgEvent := getMsgEvent(&msg); msgEvent != UFFD_EVENT_PAGEFAULT {
			u.logger.Error(ctx, "UFFD serve unexpected event type", zap.Any("event_type", msgEvent))

			return ErrUnexpectedEventType
		}

		arg := getMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))
		flags := pagefault.flags

		addr := getPagefaultAddress(&pagefault)

		offset, pagesize, err := u.ma.GetOffset(addr)
		if err != nil {
			u.logger.Error(ctx, "UFFD serve get mapping error", zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		// Handle write to missing page (WRITE flag)
		// If the event has WRITE flag, it was a write to a missing page.
		// For the write to be executed, we first need to copy the page from the source to the guest memory.
		if flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			err := u.handleMissing(ctx, fdExit.SignalExit, addr, pagesize, offset)
			if err != nil {
				return fmt.Errorf("failed to handle missing write: %w", err)
			}

			continue
		}

		// Handle read to missing page ("MISSING" flag)
		// If the event has no flags, it was a read to a missing page and we need to copy the page from the source to the guest memory.
		if flags == 0 {
			err := u.handleMissing(ctx, fdExit.SignalExit, addr, pagesize, offset)
			if err != nil {
				return fmt.Errorf("failed to handle missing: %w", err)
			}

			continue
		}

		// MINOR and WP flags are not expected as we don't register the uffd with these flags.
		return fmt.Errorf("unexpected event type: %d, closing uffd", flags)
	}
}

func (u *Userfaultfd) handleMissing(
	ctx context.Context,
	onFailure func() error,
	addr,
	pagesize uintptr,
	offset int64,
) error {
	// Capture start time outside the goroutine to include any queuing delay
	startTime := time.Now()

	u.wg.Go(func() error {
		ctx, span := tracer.Start(ctx, "uffd-page-fault")
		defer span.End()

		// The RLock must be called inside the goroutine to ensure RUnlock runs via defer,
		// even if the errgroup is cancelled or the goroutine returns early.
		// This check protects us against race condition between marking the request as missing and accessing the missingRequests tracker.
		// The Firecracker pause should return only after the requested memory is faulted in, so we don't need to guard the pagefault from the moment it is created.
		u.settleRequests.RLock()
		defer u.settleRequests.RUnlock()

		defer func() {
			if r := recover(); r != nil {
				u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", pagesize), zap.Any("panic", r))
			}
		}()

		b, sliceErr := u.src.Slice(ctx, offset, int64(pagesize))
		if sliceErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(sliceErr, signalErr)

			span.RecordError(joinedErr)
			u.logger.Error(ctx, "UFFD serve slice error", zap.Error(joinedErr))

			return fmt.Errorf("failed to read from source: %w", joinedErr)
		}

		var copyMode CULong

		copyErr := u.fd.copy(addr, pagesize, b, copyMode)
		if errors.Is(copyErr, unix.EEXIST) {
			// Page is already mapped
			span.SetAttributes(attribute.Bool("uffd.already_mapped", true))

			return nil
		}

		if copyErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(copyErr, signalErr)

			span.RecordError(joinedErr)
			u.logger.Error(ctx, "UFFD serve uffdio copy error", zap.Error(joinedErr))

			return fmt.Errorf("failed uffdio copy %w", joinedErr)
		}

		// Add the offset to the missing requests tracker.
		u.missingRequests.Add(offset)

		// Record page fault trace if enabled
		u.tracer.Record(startTime, offset, int64(pagesize), trace.TypeFault)

		return nil
	})

	return nil
}

func (u *Userfaultfd) Dirty() *block.Tracker {
	// This will be at worst cancelled when the uffd is closed.
	u.settleRequests.Lock()
	// The locking here would work even without using defer (just lock-then-unlock the mutex), but at this point let's make it lock to the clone,
	// so it is consistent even if there is a another uffd call after.
	defer u.settleRequests.Unlock()

	return u.missingRequests.Clone()
}

// Prefault proactively copies a page to guest memory at the given offset.
// This is used to speed up sandbox starts by prefetching pages that are known to be needed.
// Returns nil on success, or if the page is already mapped (EEXIST is handled gracefully).
func (u *Userfaultfd) Prefault(ctx context.Context, offset int64, data []byte) error {
	_, span := tracer.Start(ctx, "prefault page")
	defer span.End()

	// Acquire RLock to synchronize with Dirty() and ensure consistent tracker state.
	u.settleRequests.RLock()
	defer u.settleRequests.RUnlock()

	// Get host virtual address and page size for this offset
	addr, pagesize, err := u.ma.GetHostVirtAddr(offset)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("failed to get host virtual address: %w", err)
	}

	if len(data) < int(pagesize) {
		err := fmt.Errorf("data length (%d) is less than pagesize (%d)", len(data), pagesize)
		span.RecordError(err)

		return err
	}

	// Copy the page to guest memory via uffd
	var copyMode CULong
	copyErr := u.fd.copy(addr, pagesize, data, copyMode)

	// EEXIST means the page is already mapped, which is fine - another request
	// or the normal page fault handler already mapped this page
	if errors.Is(copyErr, unix.EEXIST) {
		span.SetAttributes(attribute.Bool("uffd.already_mapped", true))

		return nil
	}

	if copyErr != nil {
		span.RecordError(copyErr)

		return fmt.Errorf("failed uffdio copy: %w", copyErr)
	}

	// Add the offset to the missing requests tracker.
	u.missingRequests.Add(offset)

	return nil
}

// BlockSize returns the block size used by the source.
func (u *Userfaultfd) BlockSize() int64 {
	return u.src.BlockSize()
}
