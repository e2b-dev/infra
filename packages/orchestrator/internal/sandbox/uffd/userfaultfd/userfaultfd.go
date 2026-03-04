package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd")

const maxRequestsInProgress = 4096

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
	pageTracker pageTracker

	// We use the settleRequests to guard the prefetchTracker so we can access a consistent state of the prefetchTracker after the requests are finished.
	settleRequests  sync.RWMutex
	prefetchTracker *block.PrefetchTracker

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

// readEvents reads all available UFFD events from the file descriptor,
// returning removes and pagefaults separately.
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
			pagefaults = append(pagefaults, (*UffdPagefault)(unsafe.Pointer(&arg[0])))
		case UFFD_EVENT_REMOVE:
			removes = append(removes, (*UffdRemove)(unsafe.Pointer(&arg[0])))
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
	}

	eagainCounter := newCounterReporter(u.logger, "uffd: eagain during fd read (accumulated)")
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

		removes, pagefaults, err := u.readEvents(ctx)
		if err != nil {
			u.logger.Error(ctx, "uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		// Collect deferred pagefaults from previous iteration's goroutines.
		pagefaults = append(deferred.drain(), pagefaults...)

		// No events were found which is weird since, if we are here,
		// poll() returned with an event indicating that UFFD had something
		// for us to read. Log an error and continue
		if len(removes) == 0 && len(pagefaults) == 0 {
			eagainCounter.Increase("EAGAIN")

			continue
		}

		// We successfully read all available UFFD events.
		noDataCounter.Log(ctx)
		eagainCounter.Log(ctx)

		// First handle the UFFD_EVENT_REMOVE events
		for _, rm := range removes {
			u.pageTracker.setState(uintptr(rm.start), uintptr(rm.end), removed)
		}

		for _, pf := range pagefaults {
			// We don't handle minor page faults.
			if pf.flags&UFFD_PAGEFAULT_FLAG_MINOR != 0 {
				return fmt.Errorf("unexpected MINOR pagefault event, closing UFFD")
			}

			// We don't handle write-protection page faults, we're using asynchronous write protection.
			if pf.flags&UFFD_PAGEFAULT_FLAG_WP != 0 {
				return fmt.Errorf("unexpected WP pagefault event, closing UFFD")
			}

			addr := getPagefaultAddress(pf)
			offset, err := u.ma.GetOffset(addr)
			if err != nil {
				u.logger.Error(ctx, "UFFD serve got mapping error", zap.Error(err))

				return fmt.Errorf("failed to map: %w", err)
			}

			var source block.Slicer

			switch state := u.pageTracker.get(addr); state {
			case faulted:
				// TODO: Can we skip faulting pages that are already faulted? How does that play with prefaulting?
				continue
			case removed:
				continue
			case unfaulted:
				source = u.src
			default:
				return fmt.Errorf("unexpected pageState: %#v", state)
			}

			u.wg.Go(func() error {
				// The RLock must be called inside the goroutine to ensure RUnlock runs via defer,
				// even if the errgroup is cancelled or the goroutine returns early.
				// This check protects us against race condition between marking the request for prefetching and accessing the prefetchTracker.
				u.settleRequests.RLock()
				defer u.settleRequests.RUnlock()

				var copyMode CULong
				var accessType block.AccessType

				// Performing copy() on UFFD clears the WP bit unless we explicitly tell
				// it not to. We do that for faults caused by a read access. Write accesses
				// would anyways cause clear the write-protection bit.
				if pf.flags&UFFD_PAGEFAULT_FLAG_WRITE == 0 {
					copyMode = UFFDIO_COPY_MODE_WP
					accessType = block.Read
				} else {
					accessType = block.Write
				}

				handled, err := u.faultPage(
					ctx,
					addr,
					offset,
					source,
					fdExit.SignalExit,
					copyMode,
				)
				if err != nil {
					return err
				}

				if handled {
					u.pageTracker.setState(addr, addr+u.pageSize, faulted)
				} else {
					deferred.push(pf)
				}

				u.prefetchTracker.Add(offset, accessType)

				return nil
			})
		}
	}
}

func (u *Userfaultfd) faultPage(
	ctx context.Context,
	addr uintptr,
	offset int64,
	source block.Slicer,
	onFailure func() error,
	mode CULong,
) (bool, error) {
	span := trace.SpanFromContext(ctx)

	defer func() {
		if r := recover(); r != nil {
			u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", u.pageSize), zap.Any("panic", r))
		}
	}()

	var writeErr error

	// Write to guest memory. nil data means zero-fill
	switch {
	case source == nil && u.pageSize == header.PageSize:
		writeErr = u.fd.zero(addr, u.pageSize, mode)
	case source == nil && u.pageSize == header.HugepageSize:
		writeErr = u.fd.copy(addr, u.pageSize, header.EmptyHugePage, mode)
	default:
		b, dataErr := source.Slice(ctx, offset, int64(u.pageSize))
		if dataErr != nil {
			joinedErr := errors.Join(dataErr, safeInvoke(onFailure))

			span.RecordError(joinedErr)
			u.logger.Error(ctx, "UFFD serve data fetch error", zap.Error(joinedErr))

			return false, fmt.Errorf("failed to read from source: %w", joinedErr)
		}

		writeErr = u.fd.copy(addr, u.pageSize, b, mode)
	}

	// Page is already mapped.
	// Probably because we have already pre-faulted it. Otherwise, we should not
	// try to handle a page fault for the same address twice, since we are now
	// tracking the state of pages.
	if errors.Is(writeErr, unix.EEXIST) {
		span.SetAttributes(attribute.Bool("uffd.already_mapped", true))

		return true, nil
	}

	if errors.Is(writeErr, unix.EAGAIN) {
		// This happens when a remove event arrives in the UFFD file descriptor while
		// we are trying to copy()/zero() a page. We need to read all the events from
		// file descriptor and try again.
		u.logger.Debug(ctx, "UFFD page write EAGAIN, deferring", zap.Uintptr("addr", addr))

		return false, nil
	}

	if writeErr != nil {
		joinedErr := errors.Join(writeErr, safeInvoke(onFailure))

		span.RecordError(joinedErr)
		u.logger.Error(ctx, "UFFD serve uffdio copy error", zap.Error(joinedErr))

		return false, fmt.Errorf("failed uffdio copy %w", joinedErr)
	}

	return true, nil
}

func (u *Userfaultfd) PrefetchData() block.PrefetchData {
	// This will be at worst cancelled when the uffd is closed.
	u.settleRequests.Lock()
	u.settleRequests.Unlock() //nolint:staticcheck // SA2001: intentional — we just need to settle the read locks.

	return u.prefetchTracker.PrefetchData()
}

func (u *Userfaultfd) Close() error {
	return u.fd.close()
}
