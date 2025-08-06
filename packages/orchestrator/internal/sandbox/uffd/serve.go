package uffd

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/loopholelabs/userfaultfd-go/pkg/constants"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

type GuestRegionUffdMapping struct {
	BaseHostVirtAddr uintptr `json:"base_host_virt_addr"`
	Size             uintptr `json:"size"`
	Offset           uintptr `json:"offset"`
	PageSize         uintptr `json:"page_size_kib"`
}

func getMapping(addr uintptr, mappings []GuestRegionUffdMapping) (*GuestRegionUffdMapping, error) {
	for _, m := range mappings {
		if addr < m.BaseHostVirtAddr || m.BaseHostVirtAddr+m.Size <= addr {
			// Outside the mapping
			continue
		}

		return &m, nil
	}

	return nil, fmt.Errorf("address %d not found in any mapping", addr)
}

func Serve(
	uffd int,
	mappings mapping.Mappings,
	src *block.TrackedSliceDevice,
	fd uintptr,
	stop func() error,
	sandboxId string,
) error {
	pollFds := []unix.PollFd{
		{Fd: int32(uffd), Events: unix.POLLIN},
		{Fd: int32(fd), Events: unix.POLLIN},
	}

	var eg errgroup.Group

outerLoop:
	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				zap.L().Debug("uffd: interrupted polling, going back to polling", logger.WithSandboxID(sandboxId))

				continue
			}

			if err == unix.EAGAIN {
				zap.L().Debug("uffd: eagain during polling, going back to polling", logger.WithSandboxID(sandboxId))

				continue
			}

			zap.L().Error("UFFD serve polling error", logger.WithSandboxID(sandboxId), zap.Error(err))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := eg.Wait()
			if errMsg != nil {
				zap.L().Warn("UFFD fd exit error while waiting for goroutines to finish", logger.WithSandboxID(sandboxId), zap.Error(errMsg))

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
			zap.L().Debug("uffd: no data in fd, going back to polling", logger.WithSandboxID(sandboxId))

			continue
		}

		buf := make([]byte, unsafe.Sizeof(constants.UffdMsg{}))

		for {
			n, err := syscall.Read(uffd, buf)
			if err == syscall.EINTR {
				zap.L().Debug("uffd: interrupted read, reading again", logger.WithSandboxID(sandboxId))

				continue
			}

			if err == nil {
				// There is no error so we can proceed.
				break
			}

			if err == syscall.EAGAIN {
				zap.L().Debug("uffd: eagain error, going back to polling", logger.WithSandboxID(sandboxId), zap.Error(err), zap.Int("read_bytes", n))

				// Continue polling the fd.
				continue outerLoop
			}

			zap.L().Error("uffd: read error", logger.WithSandboxID(sandboxId), zap.Error(err))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := *(*constants.UffdMsg)(unsafe.Pointer(&buf[0]))
		if constants.GetMsgEvent(&msg) != constants.UFFD_EVENT_PAGEFAULT {
			zap.L().Error("UFFD serve unexpected event type", logger.WithSandboxID(sandboxId), zap.Any("event_type", constants.GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := constants.GetMsgArg(&msg)
		pagefault := (*(*constants.UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := constants.GetPagefaultAddress(&pagefault)

		offset, pagesize, err := mappings.GetRange(uintptr(addr))
		if err != nil {
			zap.L().Error("UFFD serve get mapping error", logger.WithSandboxID(sandboxId), zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		eg.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					zap.L().Error("UFFD serve panic", logger.WithSandboxID(sandboxId), zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))
					fmt.Printf("[sandbox %s]: recovered from panic in uffd serve (offset: %d, pagesize: %d): %v\n", sandboxId, offset, pagesize, r)
				}
			}()

			b, err := src.Slice(offset, pagesize)
			if err != nil {

				stop()

				zap.L().Error("UFFD serve slice error", logger.WithSandboxID(sandboxId), zap.Error(err))

				return fmt.Errorf("failed to read from source: %w", err)
			}

			cpy := constants.NewUffdioCopy(
				b,
				addr&^constants.CULong(pagesize-1),
				constants.CULong(pagesize),
				0,
				0,
			)

			if _, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				uintptr(uffd),
				constants.UFFDIO_COPY,
				uintptr(unsafe.Pointer(&cpy)),
			); errno != 0 {
				if errno == unix.EEXIST {
					zap.L().Debug("UFFD serve page already mapped", logger.WithSandboxID(sandboxId), zap.Any("offset", offset), zap.Any("pagesize", pagesize))

					// Page is already mapped
					return nil
				}

				stop()

				zap.L().Error("UFFD serve uffdio copy error", logger.WithSandboxID(sandboxId), zap.Error(err))

				return fmt.Errorf("failed uffdio copy %w", errno)
			}

			return nil
		})
	}
}
