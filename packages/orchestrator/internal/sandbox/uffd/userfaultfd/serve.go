package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func (u *Userfaultfd) Serve(
	ctx context.Context,
	fdExit *fdexit.FdExit,
) error {
	pollFds := []unix.PollFd{
		{Fd: int32(u.fd), Events: unix.POLLIN},
		{Fd: fdExit.Reader(), Events: unix.POLLIN},
	}

outerLoop:
	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				u.logger.Debug("uffd: interrupted polling, going back to polling")

				continue
			}

			if err == unix.EAGAIN {
				u.logger.Debug("uffd: eagain during polling, going back to polling")

				continue
			}

			u.logger.Error("UFFD serve polling error", zap.Error(err))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := u.wg.Wait()
			if errMsg != nil {
				u.logger.Warn("UFFD fd exit error while waiting for goroutines to finish", zap.Error(errMsg))

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
			u.logger.Debug("uffd: no data in fd, going back to polling")

			continue
		}

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		for {
			n, err := syscall.Read(int(u.fd), buf)
			if err == syscall.EINTR {
				u.logger.Debug("uffd: interrupted read, reading again")

				continue
			}

			if err == nil {
				// There is no error so we can proceed.
				break
			}

			if err == syscall.EAGAIN {
				u.logger.Debug("uffd: eagain error, going back to polling", zap.Error(err), zap.Int("read_bytes", n))

				// Continue polling the fd.
				continue outerLoop
			}

			u.logger.Error("uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := *(*UffdMsg)(unsafe.Pointer(&buf[0]))
		if GetMsgEvent(&msg) != UFFD_EVENT_PAGEFAULT {
			u.logger.Error("UFFD serve unexpected event type", zap.Any("event_type", GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := GetMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))
		flags := pagefault.flags

		addr := GetPagefaultAddress(&pagefault)

		offset, pagesize, err := u.ma.GetOffset(addr)
		if err != nil {
			u.logger.Error("UFFD serve get mapping error", zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		// Handle write to write protected page (WP+WRITE flag)
		if flags&UFFD_PAGEFAULT_FLAG_WP != 0 && flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			u.handleWriteProtection(addr, offset, pagesize)

			continue
		}

		// Handle write to missing page (WRITE flag)
		if flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			u.handleMissing(ctx, fdExit.SignalExit, addr, offset, pagesize, true)

			continue
		}

		// Handle read to missing page (MISSING flag)
		if flags == 0 {
			u.handleMissing(ctx, fdExit.SignalExit, addr, offset, pagesize, false)

			continue
		}

		u.logger.Warn("UFFD serve unexpected event type", zap.Any("event_type", flags))
	}
}

func (u *Userfaultfd) handleMissing(
	ctx context.Context,
	onFailure func() error,
	addr uintptr,
	offset int64,
	pagesize uint64,
	write bool,
) {
	if write {
		u.writeRequests.Add(offset)

		u.writesInProgress.Add()
	} else {
		u.missingRequests.Add(offset)
	}

	u.wg.Go(func() error {
		defer func() {
			if r := recover(); r != nil {
				u.logger.Error("UFFD serve panic", zap.Any("pagesize", pagesize), zap.Any("panic", r))
			}
		}()

		defer func() {
			if write {
				u.writesInProgress.Done()
			}
		}()

		var b []byte

		if u.disabled.Load() {
			b = header.EmptyHugePage[:pagesize]
		} else {
			sliceB, sliceErr := u.src.Slice(ctx, offset, int64(pagesize))
			if sliceErr != nil {
				signalErr := onFailure()

				joinedErr := errors.Join(sliceErr, signalErr)

				u.logger.Error("UFFD serve slice error", zap.Error(joinedErr))

				return fmt.Errorf("failed to read from source: %w", joinedErr)
			}

			b = sliceB
		}
		var copyMode CULong

		if !write {
			copyMode |= UFFDIO_COPY_MODE_WP
		}

		copyErr := u.copy(addr, b, pagesize, copyMode)
		if errors.Is(copyErr, unix.EEXIST) {
			u.logger.Debug("UFFD serve page already mapped", zap.Any("offset", addr), zap.Any("pagesize", pagesize))

			// Page is already mapped

			return nil
		}

		if copyErr != nil {
			signalErr := onFailure()

			joinedErr := errors.Join(copyErr, signalErr)

			u.logger.Error("UFFD serve uffdio copy error", zap.Error(joinedErr))

			return fmt.Errorf("failed uffdio copy %w", joinedErr)
		}

		// We mark the page as dirty if it was a write to a page that was not already mapped.
		if write {
			u.dirty.Add(offset)
		}

		return nil
	})
}

func (u *Userfaultfd) handleWriteProtection(addr uintptr, offset int64, pagesize uint64) {
	if !u.writeRequests.Add(offset) {
		return
	}

	u.writesInProgress.Add()

	u.wg.Go(func() error {
		defer func() {
			if r := recover(); r != nil {
				u.logger.Error("UFFD remove write protection panic", zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))
			}
		}()

		defer u.writesInProgress.Done()

		wpErr := u.RemoveWriteProtection(addr, pagesize)
		if wpErr != nil {
			return fmt.Errorf("error removing write protection from page %d", addr)
		}

		// We mark the page as dirty if it was a write to a page that was already mapped.
		u.dirty.Add(offset)

		return nil
	})
}
