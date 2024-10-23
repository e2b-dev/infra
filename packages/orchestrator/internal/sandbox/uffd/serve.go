package uffd

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg"

	"github.com/loopholelabs/userfaultfd-go/pkg/constants"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

const maxEagainAttempts = 32

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

func Serve(uffd int, mappings []GuestRegionUffdMapping, src *blockStorage.BlockStorage, fd uintptr, stop func() error, sandboxId string) error {
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
				continue
			}

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := eg.Wait()
			if errMsg != nil {
				return fmt.Errorf("failed to handle uffd: %w", errMsg)
			}

			return nil
		}

		buf := make([]byte, unsafe.Sizeof(constants.UffdMsg{}))

		var i int

		for {
			_, err := syscall.Read(uffd, buf)
			if err == nil {
				break
			}

			if err == syscall.EAGAIN {
				if i > maxEagainAttempts {
					return fmt.Errorf("too many uffd read attempts, last error: %w", err)
				}

				i++

				continue
			}

			return fmt.Errorf("failed to read: %w", err)
		}

		eg.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[sandbox %s]: recovered from panic in uffd serve: %v\n", sandboxId, r)
				}
			}()

			msg := (*(*constants.UffdMsg)(unsafe.Pointer(&buf[0])))
			if constants.GetMsgEvent(&msg) != constants.UFFD_EVENT_PAGEFAULT {
				stop()

				return ErrUnexpectedEventType
			}

			arg := constants.GetMsgArg(&msg)
			pagefault := (*(*constants.UffdPagefault)(unsafe.Pointer(&arg[0])))

			addr := constants.GetPagefaultAddress(&pagefault)

			mapping, err := getMapping(uintptr(addr), mappings)
			if err != nil {
				stop()

				return fmt.Errorf("failed to map: %w", err)
			}

			offset := uint64(mapping.Offset + uintptr(addr) - mapping.BaseHostVirtAddr)
			pagesize := uint64(mapping.PageSize)

			b, close, err := src.ReadRaw(int64(offset), int64(pagesize))
			if err != nil {
				stop()

				return fmt.Errorf("failed to read from source: %w", err)
			}

			defer close()

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

				return fmt.Errorf("failed uffdio copy %w", errno)
			}

			return nil
		})
	}
}
