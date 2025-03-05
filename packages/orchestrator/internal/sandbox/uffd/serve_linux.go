//go:build linux
// +build linux

package uffd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"unsafe"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/willf/bitset"
)

/*
#include <sys/syscall.h>
#include <fcntl.h>
#include <linux/userfaultfd.h>

struct uffd_pagefault {
	__u64	flags;
	__u64	address;
	__u32 ptid;
};
*/
import "C"

const (
	NR_userfaultfd = C.__NR_userfaultfd

	UFFD_API             = C.UFFD_API
	UFFD_EVENT_PAGEFAULT = C.UFFD_EVENT_PAGEFAULT

	UFFDIO_REGISTER_MODE_MISSING = C.UFFDIO_REGISTER_MODE_MISSING

	UFFDIO_API      = 3222841919 // From <linux/userfaultfd.h> macro
	UFFDIO_REGISTER = 3223366144 // From <linux/userfaultfd.h> macro
	UFFDIO_COPY     = 3223890435 // From <linux/userfaultfd.h> macro
)

type (
	CULong = C.ulonglong
	CUChar = C.uchar
	CLong  = C.longlong

	UffdMsg       = C.struct_uffd_msg
	UffdPagefault = C.struct_uffd_pagefault

	UffdioAPI      = C.struct_uffdio_api
	UffdioRegister = C.struct_uffdio_register
	UffdioRange    = C.struct_uffdio_range
	UffdioCopy     = C.struct_uffdio_copy
)

func NewUffdioAPI(api, features CULong) UffdioAPI {
	return UffdioAPI{
		api:      api,
		features: features,
	}
}

func NewUffdioRegister(start, length, mode CULong) UffdioRegister {
	return UffdioRegister{
		_range: UffdioRange{
			start: start,
			len:   length,
		},
		mode: mode,
	}
}

func NewUffdioCopy(b []byte, address CULong, pagesize CULong, mode CULong, copy CLong) UffdioCopy {
	return UffdioCopy{
		src:  CULong(uintptr(unsafe.Pointer(&b[0]))),
		dst:  address &^ CULong(pagesize-1),
		len:  pagesize,
		mode: mode,
		copy: copy,
	}
}

func GetMsgEvent(msg *UffdMsg) CUChar {
	return msg.event
}

func GetMsgArg(msg *UffdMsg) [24]byte {
	return msg.arg
}

func GetPagefaultAddress(pagefault *UffdPagefault) CULong {
	return pagefault.address
}

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

outerLoop:
	for {
		if _, err := unix.Poll(
			pollFds,
			-1,
		); err != nil {
			if err == unix.EINTR {
				zap.L().Debug("uffd: interrupted polling, going back to polling", zap.String("sandbox_id", sandboxId))

				continue
			}

			if err == unix.EAGAIN {
				zap.L().Debug("uffd: eagain during polling, going back to polling", zap.String("sandbox_id", sandboxId))

				continue
			}

			zap.L().Error("UFFD serve polling error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

			return fmt.Errorf("failed polling: %w", err)
		}

		exitFd := pollFds[1]
		if exitFd.Revents&unix.POLLIN != 0 {
			errMsg := eg.Wait()
			if errMsg != nil {
				zap.L().Warn("UFFD fd exit error while waiting for goroutines to finish", zap.String("sandbox_id", sandboxId), zap.Error(errMsg), zap.String("node_id", consul.ClientID))

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
			zap.L().Debug("uffd: no data in fd, going back to polling", zap.String("sandbox_id", sandboxId))

			continue
		}

		buf := make([]byte, unsafe.Sizeof(UffdMsg{}))

		for {
			n, err := syscall.Read(uffd, buf)
			if err == syscall.EINTR {
				zap.L().Debug("uffd: interrupted read, reading again", zap.String("sandbox_id", sandboxId))

				continue
			}

			if err == nil {
				// There is no error so we can proceed.
				break
			}

			if err == syscall.EAGAIN {
				zap.L().Debug("uffd: eagain error, going back to polling", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID), zap.Int("read_bytes", n))

				// Continue polling the fd.
				continue outerLoop
			}

			zap.L().Error("uffd: read error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

			return fmt.Errorf("failed to read: %w", err)
		}

		msg := (*(*UffdMsg)(unsafe.Pointer(&buf[0])))
		if GetMsgEvent(&msg) != UFFD_EVENT_PAGEFAULT {
			zap.L().Error("UFFD serve unexpected event type", zap.String("sandbox_id", sandboxId), zap.String("node_id", consul.ClientID), zap.Any("event_type", GetMsgEvent(&msg)))

			return ErrUnexpectedEventType
		}

		arg := GetMsgArg(&msg)
		pagefault := (*(*UffdPagefault)(unsafe.Pointer(&arg[0])))

		addr := GetPagefaultAddress(&pagefault)

		mapping, err := getMapping(uintptr(addr), mappings)
		if err != nil {
			zap.L().Error("UFFD serve get mapping error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

			return fmt.Errorf("failed to map: %w", err)
		}

		offset := int64(mapping.Offset + uintptr(addr) - mapping.BaseHostVirtAddr)
		pagesize := int64(mapping.PageSize)

		eg.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					zap.L().Error("UFFD serve panic", zap.String("sandbox_id", sandboxId), zap.String("node_id", consul.ClientID), zap.Any("offset", offset), zap.Any("pagesize", pagesize), zap.Any("panic", r))
					fmt.Printf("[sandbox %s]: recovered from panic in uffd serve (offset: %d, pagesize: %d): %v\n", sandboxId, offset, pagesize, r)
				}
			}()

			b, err := src.Slice(offset, pagesize)
			if err != nil {

				stop()

				zap.L().Error("UFFD serve slice error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

				return fmt.Errorf("failed to read from source: %w", err)
			}

			cpy := NewUffdioCopy(
				b,
				addr&^CULong(pagesize-1),
				CULong(pagesize),
				0,
				0,
			)

			if _, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				uintptr(uffd),
				UFFDIO_COPY,
				uintptr(unsafe.Pointer(&cpy)),
			); errno != 0 {
				if errno == unix.EEXIST {
					zap.L().Debug("UFFD serve page already mapped", zap.String("sandbox_id", sandboxId), zap.String("node_id", consul.ClientID), zap.Any("offset", offset), zap.Any("pagesize", pagesize))

					// Page is already mapped
					return nil
				}

				stop()

				zap.L().Error("UFFD serve uffdio copy error", zap.String("sandbox_id", sandboxId), zap.Error(err), zap.String("node_id", consul.ClientID))

				return fmt.Errorf("failed uffdio copy %w", errno)
			}

			return nil
		})
	}
}

const (
	uffdMsgListenerTimeout = 10 * time.Second
	fdSize                 = 4
	mappingsSize           = 1024
)

type UffdSetup struct {
	Mappings []GuestRegionUffdMapping
	Fd       uintptr
}

func (u *Uffd) TrackAndReturnNil() error {
	return u.lis.Close()
}

type Uffd struct {
	Exit  chan error
	Ready chan struct{}

	exitReader *os.File
	exitWriter *os.File

	Stop func() error

	lis *net.UnixListener

	memfile    *block.TrackedSliceDevice
	socketPath string
}

func (u *Uffd) Disable() error {
	return u.memfile.Disable()
}

func (u *Uffd) Dirty() *bitset.BitSet {
	return u.memfile.Dirty()
}

func New(memfile block.ReadonlyDevice, socketPath string, blockSize int64) (*Uffd, error) {
	pRead, pWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create exit fd: %w", err)
	}

	trackedMemfile, err := block.NewTrackedSliceDevice(blockSize, memfile)
	if err != nil {
		return nil, fmt.Errorf("failed to create tracked slice device: %w", err)
	}

	return &Uffd{
		Exit:       make(chan error, 1),
		Ready:      make(chan struct{}, 1),
		exitReader: pRead,
		exitWriter: pWrite,
		memfile:    trackedMemfile,
		socketPath: socketPath,
		Stop: sync.OnceValue(func() error {
			_, writeErr := pWrite.Write([]byte{0})
			if writeErr != nil {
				return fmt.Errorf("failed write to exit writer: %w", writeErr)
			}

			return nil
		}),
	}, nil
}

func (u *Uffd) Start(sandboxId string) error {
	lis, err := net.ListenUnix("unix", &net.UnixAddr{Name: u.socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("failed listening on socket: %w", err)
	}

	u.lis = lis

	err = os.Chmod(u.socketPath, 0o777)
	if err != nil {
		return fmt.Errorf("failed setting socket permissions: %w", err)
	}

	go func() {
		// TODO: If the handle function fails, we should kill the sandbox
		handleErr := u.handle(sandboxId)
		closeErr := u.lis.Close()
		writerErr := u.exitWriter.Close()

		u.Exit <- errors.Join(handleErr, closeErr, writerErr)

		close(u.Ready)
		close(u.Exit)
	}()

	return nil
}

func (u *Uffd) receiveSetup() (*UffdSetup, error) {
	err := u.lis.SetDeadline(time.Now().Add(uffdMsgListenerTimeout))
	if err != nil {
		return nil, fmt.Errorf("failed setting listener deadline: %w", err)
	}

	conn, err := u.lis.Accept()
	if err != nil {
		return nil, fmt.Errorf("failed accepting firecracker connection: %w", err)
	}

	unixConn := conn.(*net.UnixConn)

	mappingsBuf := make([]byte, mappingsSize)
	uffdBuf := make([]byte, syscall.CmsgSpace(fdSize))

	numBytesMappings, numBytesFd, _, _, err := unixConn.ReadMsgUnix(mappingsBuf, uffdBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to read unix msg from connection: %w", err)
	}

	mappingsBuf = mappingsBuf[:numBytesMappings]

	var mappings []GuestRegionUffdMapping

	err = json.Unmarshal(mappingsBuf, &mappings)
	if err != nil {
		return nil, fmt.Errorf("failed parsing memory mapping data: %w", err)
	}

	controlMsgs, err := syscall.ParseSocketControlMessage(uffdBuf[:numBytesFd])
	if err != nil {
		return nil, fmt.Errorf("failed parsing control messages: %w", err)
	}

	if len(controlMsgs) != 1 {
		return nil, fmt.Errorf("expected 1 control message containing UFFD: found %d", len(controlMsgs))
	}

	fds, err := syscall.ParseUnixRights(&controlMsgs[0])
	if err != nil {
		return nil, fmt.Errorf("failed parsing unix write: %w", err)
	}

	if len(fds) != 1 {
		return nil, fmt.Errorf("expected 1 fd: found %d", len(fds))
	}

	return &UffdSetup{
		Mappings: mappings,
		Fd:       uintptr(fds[0]),
	}, nil
}

func (u *Uffd) handle(sandboxId string) (err error) {
	setup, err := u.receiveSetup()
	if err != nil {
		return fmt.Errorf("failed to receive setup message from firecracker: %w", err)
	}

	uffd := setup.Fd
	defer func() {
		closeErr := syscall.Close(int(uffd))
		if closeErr != nil {
			zap.L().Error("failed to close uffd", zap.String("sandbox_id", sandboxId), zap.String("socket_path", u.socketPath), zap.Error(closeErr))
		}
	}()

	u.Ready <- struct{}{}

	err = Serve(int(uffd), setup.Mappings, u.memfile, u.exitReader.Fd(), u.Stop, sandboxId)
	if err != nil {
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}
