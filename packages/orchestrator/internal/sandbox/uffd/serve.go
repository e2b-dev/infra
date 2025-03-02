package uffd

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/loopholelabs/userfaultfd-go/pkg/constants"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

const (
	maxEagainAttempts = 4096
	eagainDelay       = 50 * time.Microsecond
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
		if !(addr >= m.BaseHostVirtAddr && addr < m.BaseHostVirtAddr+m.Size) {
			continue
		}

		return &m, nil
	}

	return nil, fmt.Errorf("address %d not found in any mapping", addr)
}

func Serve(uffd int, mappings []GuestRegionUffdMapping, src *block.TrackedSliceDevice, fd uintptr, stop func() error, sandboxId string) error {
	pollFds := []unix.PollFd{
		{Fd: int32(uffd), Events: unix.POLLIN},
		{Fd: int32(fd), Events: unix.POLLIN},
	}

	var eg errgroup.Group

	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				zap.L().Debug("uffd: interrupted polling, going back to polling", zap.String("sandbox_id", sandboxId))

				continue
			}

			zap.L().Info("UFFD serve polling error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := eg.Wait()
			if errMsg != nil {
				zap.L().Info("UFFD fd exit error", zap.String("sandbox_id", sandboxId), zap.Error(errMsg), zap.String("node_id", consul.ClientID))

				return fmt.Errorf("failed to handle uffd: %w", errMsg)
			}

			zap.L().Info("UFFD fd exit", zap.String("sandbox_id", sandboxId), zap.Error(errMsg), zap.String("node_id", consul.ClientID))

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
			// TODO: Also check for data != 0 in the syscall.Read loop
			zap.L().Debug("uffd: no data in fd, going back to polling", zap.String("sandbox_id", sandboxId))

			continue
		}

		buf := make([]byte, unsafe.Sizeof(constants.UffdMsg{}))

		var i int

	readLoop:
		for {
			n, err := syscall.Read(uffd, buf)
			if err == syscall.EINTR {
				zap.L().Debug("uffd: interrupted read, going back to polling", zap.String("sandbox_id", sandboxId))

				i = 0

				continue
			}

			if err == nil {
				break
			}

			if n == 0 {
				// If there is no data, go back to polling the fd.
				zap.L().Debug("uffd: no data read from fd, going back to polling", zap.String("sandbox_id", sandboxId))

				break readLoop
			}

			if err == syscall.EAGAIN {
				if i > maxEagainAttempts {
					zap.L().Info("UFFD serve read error, too many attempts", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

					return fmt.Errorf("too many uffd read attempts, last error: %w\n", err)
				}

				i++

				time.Sleep(eagainDelay)

				continue
			}

			zap.L().Info("UFFD serve read error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := (*(*constants.UffdMsg)(unsafe.Pointer(&buf[0])))
		if constants.GetMsgEvent(&msg) != constants.UFFD_EVENT_PAGEFAULT {
			zap.L().Info("UFFD serve unexpected event type", zap.String("sandbox_id", sandboxId), zap.String("node_id", consul.ClientID), zap.Any("event_type", constants.GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := constants.GetMsgArg(&msg)
		pagefault := (*(*constants.UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := constants.GetPagefaultAddress(&pagefault)

		mapping, err := getMapping(uintptr(addr), mappings)
		if err != nil {
			zap.L().Info("UFFD serve get mapping error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

			return fmt.Errorf("failed to map: %w", err)
		}

		offset := int64(mapping.Offset + uintptr(addr) - mapping.BaseHostVirtAddr)
		pagesize := int64(mapping.PageSize)

		eg.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					zap.L().Info("UFFD serve panic", zap.String("sandbox_id", sandboxId), zap.String("node_id", consul.ClientID), zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))
					fmt.Printf("[sandbox %s]: recovered from panic in uffd serve (offset: %d, pagesize: %d): %v\n", sandboxId, offset, pagesize, r)
				}
			}()

			b, err := src.Slice(offset, pagesize)
			if err != nil {
				stop()

				zap.L().Info("UFFD serve slice error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

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
					// Page is already mapped
					return nil
				}

				stop()

				zap.L().Info("UFFD serve uffdio copy error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

				return fmt.Errorf("failed uffdio copy %w", errno)
			}

			return nil
		})
	}
}
