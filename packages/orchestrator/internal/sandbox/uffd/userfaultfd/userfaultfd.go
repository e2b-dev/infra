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

	// We don't skip the already mapped pages, because if the memory is swappable the page *might* under some conditions be mapped out.
	// For hugepages this should not be a problem, but might theoretically happen to normal pages with swap
	missingRequests *block.Tracker
	// We use the settleRequests to guard the missingRequests so we can access a consistent state of the missingRequests after the requests are finished.
	settleRequests sync.RWMutex

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
		missingRequests: block.NewTracker(blockSize),
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

// readEvents reads all available UFFD events from the file descriptor.
// Returns a queue with the events read, or an error.
func (u *Userfaultfd) readEvents(ctx context.Context) (*queue, error) {
	buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

	queue := newQueue()
	for {
		n, err := syscall.Read(int(u.fd), buf)
		if errors.Is(err, syscall.EINTR) {
			u.logger.Debug(ctx, "uffd: interrupted read. Reading again")

			continue
		}

		if errors.Is(err, syscall.EAGAIN) {
			// EAGAIN means that we have drained all the available events for the file descriptro.
			// We are done.
			break
		}

		if err != nil {
			return nil, fmt.Errorf("failed reading uffd: %w", err)
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
			pagefault := (*UffdPagefault)(unsafe.Pointer(&arg[0]))
			queue.push(pagefault)
		case UFFD_EVENT_REMOVE:
			remove := (*UffdRemove)(unsafe.Pointer(&arg[0]))
			queue.push(remove)
		default:
			return nil, ErrUnexpectedEventType
		}
	}

	return queue, nil
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

	deferredEvents := newQueue()

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

		events, err := u.readEvents(ctx)
		if err != nil {
			u.logger.Error(ctx, "uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		events.prepend(deferredEvents)
		// This is not racy because there shouldn't be any goroutines running at the moment.
		deferredEvents.reset()

		// No events were found which is weird since, if we are here,
		// poll() returned with an event indicating that UFFD had something
		// for us to read. Log an error and continue
		if events.size() == 0 {
			eagainCounter.Increase("EAGAIN")

			continue
		}

		// We successfully read all available UFFD events.
		noDataCounter.Log(ctx)
		eagainCounter.Log(ctx)

		// First handle the UFFD_EVENT_REMOVE events
		for rm := range each[*UffdRemove](events) {
			u.pageTracker.setState(uintptr(rm.start), uintptr(rm.end), removed)
		}

		for pf := range each[*UffdPagefault](events) {
			// We don't handle minor page faults.
			if pf.flags&UFFD_PAGEFAULT_FLAG_MINOR != 0 {
				return fmt.Errorf("unexpected MINOR pagefault event, closing UFFD")
			}

			// We don't handle write-protection page faults, we're using asynchronous write protection.
			if pf.flags&UFFD_PAGEFAULT_FLAG_WP != 0 {
				return fmt.Errorf("unexecpted WP pagefault event, closuing UFFD")
			}

			addr := getPagefaultAddress(pf)
			offset, err := u.ma.GetOffset(addr)
			if err != nil {
				u.logger.Error(ctx, "UFFD serve got mapping error", zap.Error(err))

				return fmt.Errorf("failed to map: %w", err)
			}

			state := u.pageTracker.get(addr)
			switch state {
			case faulted:
				continue
			case removed:
				u.wg.Go(func() error {
					handled, err := u.faultPage(ctx, addr, offset, nil, fdExit.SignalExit, block.Remove)
					if err != nil {
						return err
					}

					if !handled {
						deferredEvents.push(pf)
					}

					return nil
				})
			case unfaulted:
				var accessType block.AccessType
				if pf.flags&UFFD_PAGEFAULT_FLAG_WRITE == 0 {
					accessType = block.Read
				} else {
					accessType = block.Write
				}

				u.wg.Go(func() error {
					handled, err := u.faultPage(ctx, addr, offset, u.src, fdExit.SignalExit, accessType)
					if err != nil {
						return err
					}

					if !handled {
						deferredEvents.push(pf)
					}

					return nil
				})
			default:
				return fmt.Errorf("unexpected pageState: %#v", state)
			}
		}
	}
}

func (u *Userfaultfd) faultPage(
	ctx context.Context,
	addr uintptr,
	offset int64,
	source block.Slicer,
	onFailure func() error,
	accessType block.AccessType,
) (bool, error) {
	span := trace.SpanFromContext(ctx)

	// The RLock must be called inside the goroutine to ensure RUnlock runs via defer,
	// even if the errgroup is cancelled or the goroutine returns early.
	// This check protects us against race condition between marking the request as missing and accessing the missingRequests tracker.
	// The Firecracker pause should return only after the requested memory is faulted in, so we don't need to guard the pagefault from the moment it is created.
	u.settleRequests.RLock()
	defer u.settleRequests.RUnlock()

	defer func() {
		if r := recover(); r != nil {
			u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", u.pageSize), zap.Any("panic", r))
		}
	}()

	var writeErr error
	var copyMode CULong

	// Performing copy() on UFFD clears the WP bit unless we explicitly tell
	// it not to. We do that for faults caused by a read access. Write accesses
	// would anyways cause clear the write-protection bit.
	if accessType != block.Write {
		copyMode = UFFDIO_COPY_MODE_WP
	}

	// Write to guest memory. nil data means zero-fill
	switch {
	case source == nil && u.pageSize == header.PageSize:
		writeErr = u.fd.zero(addr, u.pageSize, copyMode)
	case source == nil && u.pageSize == header.HugepageSize:
		writeErr = u.fd.copy(addr, u.pageSize, header.EmptyHugePage, 0)
	default:
		b, dataErr := source.Slice(ctx, offset, int64(u.pageSize))
		if dataErr != nil {
			joinedErr := errors.Join(dataErr, safeInvoke(onFailure))

			span.RecordError(joinedErr)
			u.logger.Error(ctx, "UFFD serve data fetch error", zap.Error(joinedErr))

			return false, fmt.Errorf("failed to read from source: %w", joinedErr)
		}

		writeErr = u.fd.copy(addr, u.pageSize, b, copyMode)
	}

	// Page is already mapped.
	// Probably because we have already pre-faulted it. Otherwise, we should not
	// try to handle a page fault for the same address twich, since we are now
	// tracking the state of pages.
	if errors.Is(writeErr, unix.EEXIST) {
		u.pageTracker.setState(addr, addr+u.pageSize, faulted)
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

	// Add the offset to the missing requests tracker with metadata.
	u.missingRequests.Add(offset)
	u.prefetchTracker.Add(offset, accessType)
	u.pageTracker.setState(addr, addr+u.pageSize, faulted)

	return true, nil
}

func (u *Userfaultfd) PrefetchData() block.PrefetchData {
	// This will be at worst cancelled when the uffd is closed.
	u.settleRequests.Lock()
	// The locking here would work even without using defer (just lock-then-unlock the mutex), but at this point let's make it lock to the clone,
	// so it is consistent even if there is a another uffd call after.
	defer u.settleRequests.Unlock()

	return u.prefetchTracker.PrefetchData()
}

func (u *Userfaultfd) Close() error {
	return u.fd.close()
}
