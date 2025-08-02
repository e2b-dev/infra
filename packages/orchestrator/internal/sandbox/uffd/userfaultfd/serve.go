package userfaultfd

import (
	"fmt"
	"syscall"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/firecracker"
)

func (h *userfaultfd) Serve(
	mappings firecracker.Mappings,
	src block.Slicer,
	fdExit *FdExit,
	fields ...zap.Field,
) error {
	pollFds := []unix.PollFd{
		{Fd: int32(h.fd), Events: unix.POLLIN},
		{Fd: fdExit.Reader(), Events: unix.POLLIN},
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
				zap.L().Debug("uffd: interrupted polling, going back to polling", fields...)

				continue
			}

			if err == unix.EAGAIN {
				zap.L().Debug("uffd: eagain during polling, going back to polling", fields...)

				continue
			}

			zap.L().Error("UFFD serve polling error", append(fields, zap.Error(err))...)

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := eg.Wait()
			if errMsg != nil {
				zap.L().Warn("UFFD fd exit error while waiting for goroutines to finish", append(fields, zap.Error(errMsg))...)

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
			zap.L().Debug("uffd: no data in fd, going back to polling", fields...)

			continue
		}

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		for {
			n, err := syscall.Read(int(h.fd), buf)
			if err == syscall.EINTR {
				zap.L().Debug("uffd: interrupted read, reading again", fields...)

				continue
			}

			if err == nil {
				// There is no error so we can proceed.
				break
			}

			if err == syscall.EAGAIN {
				zap.L().Debug("uffd: eagain error, going back to polling", append(fields, zap.Error(err), zap.Int("read_bytes", n))...)

				// Continue polling the fd.
				continue outerLoop
			}

			zap.L().Error("uffd: read error", append(fields, zap.Error(err))...)

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := (*(*UffdMsg)(unsafe.Pointer(&buf[0])))
		if GetMsgEvent(&msg) != UFFD_EVENT_PAGEFAULT {
			zap.L().Error("UFFD serve unexpected event type", append(fields, zap.Any("event_type", GetMsgEvent(&msg)))...)

			return ErrUnexpectedEventType
		}

		arg := GetMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := GetPagefaultAddress(&pagefault)

		offset, pagesize, err := mappings.GetRange(uintptr(addr))
		if err != nil {
			zap.L().Error("UFFD serve get mapping error", append(fields, zap.Error(err))...)

			return fmt.Errorf("failed to map: %w", err)
		}

		if _, ok := missingPagesBeingHandled[offset]; ok {
			continue
		}

		missingPagesBeingHandled[offset] = struct{}{}

		eg.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					zap.L().Error("UFFD serve panic", append(fields, zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))...)
					fmt.Printf("[uffd]: recovered from panic in uffd serve (offset: %d, pagesize: %d): %v\n", offset, pagesize, r)
				}
			}()

			b, err := src.Slice(offset, pagesize)
			if err != nil {
				fdExit.SignalExit()

				zap.L().Error("UFFD serve slice error", append(fields, zap.Error(err))...)

				return fmt.Errorf("failed to read from source: %w", err)
			}

			err = h.Copy(addr, b, pagesize)
			if err == unix.EEXIST {
				zap.L().Debug("UFFD serve page already mapped", append(fields, zap.Any("offset", offset), zap.Any("pagesize", pagesize))...)

				// Page is already mapped
				return nil
			}

			if err != nil {
				fdExit.SignalExit()

				zap.L().Error("UFFD serve uffdio copy error", append(fields, zap.Error(err))...)

				return fmt.Errorf("failed uffdio copy %w", err)
			}

			return nil
		})
	}
}
