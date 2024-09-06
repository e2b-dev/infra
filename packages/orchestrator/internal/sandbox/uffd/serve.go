package uffd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"

	"github.com/loopholelabs/userfaultfd-go/pkg/constants"
	"golang.org/x/sys/unix"
)

const (
	maxEagainAttempts = 32
	maxPageSize       = 2 << 20
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

func Serve(uffd int, mappings []GuestRegionUffdMapping, src io.ReaderAt, fd uintptr) error {
	pollFds := []unix.PollFd{
		{Fd: int32(uffd), Events: unix.POLLIN},
		{Fd: int32(fd), Events: unix.POLLIN},
	}

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

		msg := (*(*constants.UffdMsg)(unsafe.Pointer(&buf[0])))
		if constants.GetMsgEvent(&msg) != constants.UFFD_EVENT_PAGEFAULT {
			return ErrUnexpectedEventType
		}

		arg := constants.GetMsgArg(&msg)
		pagefault := (*(*constants.UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := constants.GetPagefaultAddress(&pagefault)

		go func(addr constants.CULong) (err error) {
			defer func() {
				if err != nil {
					fmt.Fprintf(os.Stderr, "failed to handle pagefault: %v\n", err)
				}
			}()

			mapping, err := getMapping(uintptr(addr), mappings)
			if err != nil {
				return fmt.Errorf("failed to map: %w", err)
			}

			offset := uint64(mapping.Offset + uintptr(addr) - mapping.BaseHostVirtAddr)
			pagesize := uint64(mapping.PageSize)

			pageBuf := make([]byte, pagesize)

			n, err := src.ReadAt(pageBuf, int64(offset))
			if err != nil {
				if !errors.Is(err, io.EOF) && n != 0 {
					return fmt.Errorf("failed to read from source: %w", err)
				}
			}

			cpy := constants.NewUffdioCopy(
				pageBuf,
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

				return fmt.Errorf("failed uffdio copy %w", errno)
			}

			return nil
		}(addr)
	}
}
