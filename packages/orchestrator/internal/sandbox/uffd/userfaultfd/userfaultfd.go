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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/firecracker"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

type userfaultfd struct {
	fd       uintptr
	copyMode CULong
}

// flags: syscall.O_CLOEXEC|syscall.O_NONBLOCK
func NewUserfaultfd(flags uintptr, wp bool) (*userfaultfd, error) {
	uffd, _, errno := syscall.Syscall(NR_userfaultfd, flags, 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("userfaultfd syscall failed: %v", errno)
	}

	return NewUserfaultfdFromFd(uffd, wp), nil
}

func NewUserfaultfdFromFd(fd uintptr, wp bool) *userfaultfd {
	copyMode := CULong(0)
	if wp {
		copyMode = UFFDIO_COPY_MODE_WP
	}

	return &userfaultfd{
		fd:       fd,
		copyMode: copyMode,
	}
}

// features: UFFD_FEATURE_MISSING_HUGETLBFS|UFFD_FEATURE_WP_ASYNC
// This is already called by the FC
func (u *userfaultfd) ConfigureApi(features CULong) error {
	api := NewUffdioAPI(UFFD_API, features)
	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, u.fd, UFFDIO_API, uintptr(unsafe.Pointer(&api)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_API ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

// mode: UFFDIO_REGISTER_MODE_WP|UFFDIO_REGISTER_MODE_MISSING
// This is already called by the FC, but only with the UFFDIO_REGISTER_MODE_MISSING
// We need to call it with UFFDIO_REGISTER_MODE_WP when we use both missing and wp
func (h *userfaultfd) Register(addr uintptr, size uint64, mode CULong) error {
	register := NewUffdioRegister(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, h.fd, UFFDIO_REGISTER, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_REGISTER ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

func (h *userfaultfd) writeProtect(addr uintptr, size uint64, mode CULong) error {
	register := NewUffdioWriteProtect(CULong(addr), CULong(size), mode)

	ret, _, errno := syscall.Syscall(syscall.SYS_IOCTL, h.fd, UFFDIO_WRITEPROTECT, uintptr(unsafe.Pointer(&register)))
	if errno != 0 {
		return fmt.Errorf("UFFDIO_WRITEPROTECT ioctl failed: %v (ret=%d)", errno, ret)
	}

	return nil
}

func (h *userfaultfd) RemoveWriteProtection(addr uintptr, size uint64) error {
	return h.writeProtect(addr, size, 0)
}

func (h *userfaultfd) AddWriteProtection(addr uintptr, size uint64) error {
	return h.writeProtect(addr, size, UFFDIO_WRITEPROTECT_MODE_WP)
}

// mode: UFFDIO_COPY_MODE_WP
// When we use both missing and wp, we need to use UFFDIO_COPY_MODE_WP, otherwise copying would unprotect the page
func (h *userfaultfd) Copy(addr CULong, data []byte, pagesize int64) error {
	cpy := NewUffdioCopy(data, addr&^CULong(pagesize-1), CULong(pagesize), h.copyMode, 0)

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, h.fd, UFFDIO_COPY, uintptr(unsafe.Pointer(&cpy))); errno != 0 {
		return errno
	}

	return nil
}

func (h *userfaultfd) Close() error {
	return syscall.Close(int(h.fd))
}

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
