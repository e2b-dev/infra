package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"unsafe"

	"github.com/loopholelabs/userfaultfd-go/pkg/constants"
	"github.com/loopholelabs/userfaultfd-go/pkg/mapper"
	"golang.org/x/sys/unix"
)

var ErrUnexpectedEventType = errors.New("unexpected event type")

func handleUffd(ctx context.Context, ud mapper.UFFD, start uintptr, src io.ReaderAt, pagesize int) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if _, err := unix.Poll(
				[]unix.PollFd{{
					Fd:     int32(ud),
					Events: unix.POLLIN,
				}},
				-1,
			); err != nil {
				fmt.Printf("poll error: %+v\n", err)
				return err
			}

			buf := make([]byte, unsafe.Sizeof(constants.UffdMsg{}))
			if _, err := syscall.Read(int(ud), buf); err != nil {
				fmt.Printf("read error: %+v\n", err)
				return err
			}

			msg := (*(*constants.UffdMsg)(unsafe.Pointer(&buf[0])))
			if constants.GetMsgEvent(&msg) != constants.UFFD_EVENT_PAGEFAULT {
				fmt.Printf("unexpected event type: %+v\n", ErrUnexpectedEventType)
				return ErrUnexpectedEventType
			}

			arg := constants.GetMsgArg(&msg)
			pagefault := (*(*constants.UffdPagefault)(unsafe.Pointer(&arg[0])))

			addr := constants.GetPagefaultAddress(&pagefault)

			p := make([]byte, pagesize)
			if n, err := src.ReadAt(p, int64(uintptr(addr)-start)); err != nil {
				// We always read full pages; the last read can thus `EOF` if the file isn't an exact multiple of `pagesize`
				if !(errors.Is(err, io.EOF) && n != 0) {
					return err
				}
			}

			cpy := constants.NewUffdioCopy(
				p,
				addr&^constants.CULong(pagesize-1),
				constants.CULong(pagesize),
				0,
				0,
			)

			if _, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				uintptr(ud),
				constants.UFFDIO_COPY,
				uintptr(unsafe.Pointer(&cpy)),
			); errno != 0 {
				fmt.Printf("ioctl error: %+v\n", errno)
				return fmt.Errorf("%v", errno)
			}
		}
	}
}
