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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

type GuestRegionUffdMapping struct {
	BaseHostVirtAddr uintptr `json:"base_host_virt_addr"`
	Size             uintptr `json:"size"`
	Offset           uintptr `json:"offset"`
	// This is actually in bytes—it is deprecated and they introduced "page_size"
	PageSize uintptr `json:"page_size_kib"`
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

type handler struct {
	mappings []GuestRegionUffdMapping
	uffd     uintptr
}

// features: UFFD_FEATURE_MISSING_HUGETLBFS|UFFD_FEATURE_WP_ASYNC
// This is already called by the FC
func (u *handler) ConfigureApi(features CULong) error {
	api := NewUffdioAPI(UFFD_API, features)
	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.uffd, UFFDIO_API, uintptr(unsafe.Pointer(&api)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_API ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

// mode: UFFDIO_REGISTER_MODE_WP|UFFDIO_REGISTER_MODE_MISSING
// This is already called by the FC, but only with the UFFDIO_REGISTER_MODE_MISSING
// We need to call it with UFFDIO_REGISTER_MODE_WP when we use both missing and wp
func (h *handler) Register(addr uintptr, size uint64, mode CULong) error {
	register := NewUffdioRegister(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, h.uffd, UFFDIO_REGISTER, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_REGISTER ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

func (h *handler) writeProtect(addr uintptr, size uint64, mode CULong) error {
	register := NewUffdioWriteProtect(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, h.uffd, UFFDIO_WRITEPROTECT, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_WRITEPROTECT ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

func (h *handler) RemoveWriteProtection(addr uintptr, size uint64) error {
	return h.writeProtect(addr, size, 0)
}

func (h *handler) AddWriteProtection(addr uintptr, size uint64) error {
	return h.writeProtect(addr, size, UFFDIO_WRITEPROTECT_MODE_WP)
}

// mode: UFFDIO_COPY_MODE_WP
// When we use both missing and wp, we need to use UFFDIO_COPY_MODE_WP, otherwise copying would unprotect the page
func (h *handler) Copy(addr CULong, data []byte, copyMode CULong, pagesize int64) error {
	cpy := NewUffdioCopy(data, addr&^CULong(pagesize-1), CULong(pagesize), copyMode, 0)

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, h.uffd, UFFDIO_COPY, uintptr(unsafe.Pointer(&cpy))); errno != 0 {
		return errno
	}

	return nil
}

func (h *handler) close() error {
	return syscall.Close(int(h.uffd))
}

func (h *handler) Serve(
	mappings []GuestRegionUffdMapping,
	src *block.TrackedSliceDevice,
	fd uintptr,
	stop func() error,
	sandboxId string,
) error {
	pollFds := []unix.PollFd{
		{Fd: int32(h.uffd), Events: unix.POLLIN},
		{Fd: int32(fd), Events: unix.POLLIN},
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
			n, err := syscall.Read(int(h.uffd), buf)
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

		msg := (*(*UffdMsg)(unsafe.Pointer(&buf[0])))
		if GetMsgEvent(&msg) != UFFD_EVENT_PAGEFAULT {
			zap.L().Error("UFFD serve unexpected event type", logger.WithSandboxID(sandboxId), zap.Any("event_type", GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := GetMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := GetPagefaultAddress(&pagefault)

		mapping, err := getMapping(uintptr(addr), mappings)
		if err != nil {
			zap.L().Error("UFFD serve get mapping error", logger.WithSandboxID(sandboxId), zap.Error(err))

			return fmt.Errorf("failed to map: %w", err)
		}

		offset := int64(mapping.Offset + uintptr(addr) - mapping.BaseHostVirtAddr)
		pagesize := int64(mapping.PageSize)

		if _, ok := missingPagesBeingHandled[offset]; ok {
			continue
		}

		missingPagesBeingHandled[offset] = struct{}{}

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

			err = h.Copy(addr, b, 0, pagesize)
			if err == unix.EEXIST {
				zap.L().Debug("UFFD serve page already mapped", logger.WithSandboxID(sandboxId), zap.Any("offset", offset), zap.Any("pagesize", pagesize))

				// Page is already mapped
				return nil
			}

			if err != nil {
				stop()

				zap.L().Error("UFFD serve uffdio copy error", logger.WithSandboxID(sandboxId), zap.Error(err))

				return fmt.Errorf("failed uffdio copy %w", err)
			}

			return nil
		})
	}
}
