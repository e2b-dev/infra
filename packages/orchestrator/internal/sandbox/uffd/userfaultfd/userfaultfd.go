package userfaultfd

// flowchart TD
// A[missing page] -- write (WRITE flag) --> B(COPY) --> C[dirty page]
// A -- read (MISSING flag) --> D(COPY + MODE_WP) --> E[faulted page]
// E -- write (WP+[WRITE] flag) --> F(remove MODE_WP) --> C

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const maxRequestsInProgress = 4096

var ErrUnexpectedEventType = errors.New("unexpected event type")

type uffdio interface {
	unregister(addr, size uintptr) error
	register(addr uintptr, size uint64, mode CULong) error
	copy(addr, pagesize uintptr, data []byte, mode CULong) error
	writeProtect(addr, size uintptr, mode CULong) error
	close() error
	fd() int32
}

type Userfaultfd struct {
	uffd uffdio

	src block.Slicer
	m   *memory.Mapping

	// We don't skip the already mapped pages, because if the memory is swappable the page *might* under some conditions be mapped out.
	// For hugepages this should not be a problem, but might theoretically happen to normal pages with swap
	missingRequests *block.Tracker
	writeRequests   *block.Tracker
	// We use the settleRequests to guard the missingRequests so we can access a consistent state of the missingRequests after the requests are finished.
	settleRequests sync.RWMutex

	wg errgroup.Group

	logger logger.Logger
}

// NewUserfaultfdFromFd creates a new userfaultfd instance with optional configuration.
func NewUserfaultfdFromFd(uffd uffdio, src block.Slicer, m *memory.Mapping, logger logger.Logger) (*Userfaultfd, error) {
	blockSize := src.BlockSize()

	for _, region := range m.Regions {
		if region.PageSize != uintptr(blockSize) {
			return nil, fmt.Errorf("block size mismatch: %d != %d for region %d", region.PageSize, blockSize, region.BaseHostVirtAddr)
		}

		// Register the WP for the regions.
		// The memory region is already registered (with missing pages in FC), but registering it again with bigger flag subset should merge these registration flags.
		// - https://github.com/firecracker-microvm/firecracker/blob/f335a0adf46f0680a141eb1e76fe31ac258918c5/src/vmm/src/persist.rs#L477
		// - https://github.com/bytecodealliance/userfaultfd-rs/blob/main/src/builder.rs
		err := uffd.register(
			region.BaseHostVirtAddr,
			uint64(region.Size),
			UFFDIO_REGISTER_MODE_WP|UFFDIO_REGISTER_MODE_MISSING,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to reregister memory region with write protection %d-%d: %w", region.Offset, region.Offset+region.Size, err)
		}
	}

	u := &Userfaultfd{
		uffd:            uffd,
		src:             src,
		missingRequests: block.NewTracker(blockSize),
		writeRequests:   block.NewTracker(blockSize),
		m:               m,
		logger:          logger,
	}

	// By default this was unlimited.
	// Now that we don't skip previously faulted pages we add at least some boundaries to the concurrency.
	// Also, in some brief tests, adding a limit actually improved the handling at high concurrency.
	u.wg.SetLimit(maxRequestsInProgress)

	return u, nil
}

func (u *Userfaultfd) Close() error {
	return u.uffd.close()
}

func (u *Userfaultfd) Serve(
	ctx context.Context,
	fdExit *fdexit.FdExit,
) error {
	uffd := u.uffd.fd()

	pollFds := []unix.PollFd{
		{Fd: uffd, Events: unix.POLLIN},
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

			return fdexit.ErrFdExit
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
			_, err := syscall.Read(int(uffd), buf)
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

		offset, pagesize, err := u.m.GetOffset(addr)
		if err != nil {
			u.logger.Error(ctx, "UFFD serve get mapping error", zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		// Handle write to write protected page (WP flag)
		// The documentation does not clearly mention if the WRITE flag must be present with the WP flag, even though we saw it being present in the events.
		// - https://docs.kernel.org/admin-guide/mm/userfaultfd.html#write-protect-notifications
		if flags&UFFD_PAGEFAULT_FLAG_WP != 0 {
			u.handleWriteProtected(ctx, fdExit.SignalExit, addr, pagesize, offset)

			continue
		}

		// Handle write to missing page (WRITE flag)
		// If the event has WRITE flag, it was a write to a missing page.
		// For the write to be executed, we first need to copy the page from the source to the guest memory.
		if flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			u.handleMissing(ctx, fdExit.SignalExit, addr, pagesize, offset, true)

			continue
		}

		// Handle read to missing page ("MISSING" flag)
		// If the event has no flags, it was a read to a missing page and we need to copy the page from the source to the guest memory.
		if flags == 0 {
			u.handleMissing(ctx, fdExit.SignalExit, addr, pagesize, offset, false)

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
	write bool,
) {
	u.wg.Go(func() error {
		// The RLock must be called inside the goroutine to ensure RUnlock runs via defer,
		// even if the errgroup is cancelled or the goroutine returns early.
		// This check protects us against race condition between marking the request as missing and accessing the missingRequests tracker.
		// The Firecracker pause should return only after the requested memory is copied to the guest memory, so we don't need to guard the pagefault from the moment it is created.
		u.settleRequests.RLock()
		defer u.settleRequests.RUnlock()

		defer func() {
			if r := recover(); r != nil {
				u.logger.Error(ctx, "UFFD serve panic", zap.Any("pagesize", pagesize), zap.Any("panic", r))

				signalErr := onFailure()
				if signalErr != nil {
					u.logger.Error(ctx, "UFFD handle missing failure error", zap.Error(signalErr))
				}
			}
		}()

		b, sliceErr := u.src.Slice(ctx, offset, int64(pagesize))
		if sliceErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(sliceErr, signalErr)

			u.logger.Error(ctx, "UFFD serve slice error", zap.Error(joinedErr))

			return fmt.Errorf("failed to read from source: %w", joinedErr)
		}

		var copyMode CULong

		// If the event is not WRITE, we need to add WP to the page, so we can catch the next WRITE+WP and mark the page as dirty.
		if !write {
			copyMode |= UFFDIO_COPY_MODE_WP
		}

		copyErr := u.uffd.copy(addr, pagesize, b, copyMode)
		if errors.Is(copyErr, unix.EEXIST) {
			// Page is already mapped

			return nil
		}

		if copyErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(copyErr, signalErr)

			u.logger.Error(ctx, "UFFD serve uffdio copy error", zap.Error(joinedErr))

			return fmt.Errorf("failed to copy page %d-%d %w", offset, offset+int64(pagesize), joinedErr)
		}

		// Add the offset to the missing requests tracker.
		u.missingRequests.Add(offset)

		if write {
			// Add the offset to the write requests tracker.
			u.writeRequests.Add(offset)
		}

		// Add the offset to the missing requests tracker.
		u.missingRequests.Add(offset)

		return nil
	})
}

// Userfaultfd write-protect mode currently behave differently on none ptes (when e.g. page is missing) over different types of memories (hugepages file backed, etc.).
// - https://docs.kernel.org/admin-guide/mm/userfaultfd.html#write-protect-notifications - "there will be a userfaultfd write fault message generated when writing to a missing page"
// This should not affect the handling we have in place as all events are being handled.
func (u *Userfaultfd) handleWriteProtected(ctx context.Context, onFailure func() error, addr, pagesize uintptr, offset int64) {
	u.wg.Go(func() error {
		// The RLock must be called inside the goroutine to ensure RUnlock runs via defer,
		// even if the errgroup is cancelled or the goroutine returns early.
		// This check protects us against race condition between marking the request as dirty and accessing the writeRequests tracker.
		// The Firecracker pause should return only after the requested memory is copied to the guest memory, so we don't need to guard the pagefault from the moment it is created.
		u.settleRequests.RLock()
		defer u.settleRequests.RUnlock()

		defer func() {
			if r := recover(); r != nil {
				u.logger.Error(ctx, "UFFD remove write protection panic", zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))

				signalErr := onFailure()
				if signalErr != nil {
					u.logger.Error(ctx, "UFFD handle write protected failure error", zap.Error(signalErr))
				}
			}
		}()

		// Passing 0 as the mode removes the write protection.
		wpErr := u.uffd.writeProtect(addr, pagesize, 0)
		if wpErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(wpErr, signalErr)

			u.logger.Error(ctx, "UFFD serve write protect error", zap.Error(joinedErr))

			return fmt.Errorf("failed to remove write protection from page %d-%d %w", offset, offset+int64(pagesize), joinedErr)
		}

		// Add the offset to the write requests tracker.
		u.writeRequests.Add(offset)

		return nil
	})
}

// Dirty returns the dirty pages.
func (u *Userfaultfd) Dirty() *block.Tracker {
	// This will be at worst cancelled when the uffd is closed.
	u.settleRequests.Lock()
	// The locking here would work even without using defer (just lock-then-unlock the mutex), but at this point let's make it lock to the clone,
	// so it is consistent even if there is a another uffd call after.
	defer u.settleRequests.Unlock()

	return u.writeRequests.Clone()
}
