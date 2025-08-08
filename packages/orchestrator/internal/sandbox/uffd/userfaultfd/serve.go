package userfaultfd

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
)

func (u *userfaultfd) Serve(
	mappings mapping.Mappings,
	src block.Slicer,
	fdExit *fdexit.FdExit,
	logger *zap.Logger,
) error {
	pollFds := []unix.PollFd{
		{Fd: int32(u.fd), Events: unix.POLLIN},
		{Fd: int32(fdExit.Reader()), Events: unix.POLLIN},
	}

	var eg errgroup.Group

	missingPagesBeingHandled := map[int64]struct{}{}

outerLoop:
	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				logger.Debug("uffd: interrupted polling, going back to polling")

				continue
			}

			if err == unix.EAGAIN {
				logger.Debug("uffd: eagain during polling, going back to polling")

				continue
			}

			logger.Error("UFFD serve polling error", zap.Error(err))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := eg.Wait()
			if errMsg != nil {
				logger.Warn("UFFD fd exit error while waiting for goroutines to finish", zap.Error(errMsg))

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
			logger.Debug("uffd: no data in fd, going back to polling")

			continue
		}

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		for {
			n, err := syscall.Read(int(u.fd), buf)
			if err == syscall.EINTR {
				logger.Debug("uffd: interrupted read, reading again")

				continue
			}

			if err == nil {
				// There is no error so we can proceed.
				break
			}

			if err == syscall.EAGAIN {
				logger.Debug("uffd: eagain error, going back to polling", zap.Error(err), zap.Int("read_bytes", n))

				// Continue polling the fd.
				continue outerLoop
			}

			logger.Error("uffd: read error", zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := *(*UffdMsg)(unsafe.Pointer(&buf[0]))
		if GetMsgEvent(&msg) != UFFD_EVENT_PAGEFAULT {
			logger.Error("UFFD serve unexpected event type", zap.Any("event_type", GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := GetMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := GetPagefaultAddress(&pagefault)

		offset, pagesize, err := mappings.GetRange(uintptr(addr))
		if err != nil {
			logger.Error("UFFD serve get mapping error", zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		if _, ok := missingPagesBeingHandled[offset]; ok {
			continue
		}

		missingPagesBeingHandled[offset] = struct{}{}

		eg.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("UFFD serve panic", zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))
				}
			}()

			b, err := src.Slice(offset, pagesize)
			if err != nil {

				signalErr := fdExit.SignalExit()

				joinedErr := errors.Join(err, signalErr)

				logger.Error("UFFD serve slice error", zap.Error(joinedErr))

				return fmt.Errorf("failed to read from source: %w", joinedErr)
			}

			err = u.copy(addr, b, pagesize)
			if err == unix.EEXIST {
				logger.Debug("UFFD serve page already mapped", zap.Any("offset", offset), zap.Any("pagesize", pagesize))

				// Page is already mapped
				return nil
			}

			if err != nil {
				signalErr := fdExit.SignalExit()

				joinedErr := errors.Join(err, signalErr)

				logger.Error("UFFD serve uffdio copy error", zap.Error(joinedErr))

				return fmt.Errorf("failed uffdio copy %w", joinedErr)
			}

			return nil
		})
	}
}
