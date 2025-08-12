package uffd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/bits-and-blooms/bitset"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/mapping"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	uffdMsgListenerTimeout = 10 * time.Second
	fdSize                 = 4
	mappingsSize           = 1024
)

type Uffd struct {
	exitCh  chan error
	readyCh chan struct{}

	fdExit *fdexit.FdExit

	lis *net.UnixListener

	memfile    *block.TrackedSliceDevice
	socketPath string

	writeProtection bool
}

func New(memfile block.ReadonlyDevice, socketPath string, blockSize int64) (*Uffd, error) {
	trackedMemfile, err := block.NewTrackedSliceDevice(blockSize, memfile)
	if err != nil {
		return nil, fmt.Errorf("failed to create tracked slice device: %w", err)
	}

	fdExit, err := fdexit.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create fd exit: %w", err)
	}

	return &Uffd{
		exitCh:          make(chan error, 1),
		readyCh:         make(chan struct{}, 1),
		fdExit:          fdExit,
		memfile:         trackedMemfile,
		socketPath:      socketPath,
		writeProtection: true,
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
		fdExitErr := u.fdExit.Close()

		u.exitCh <- errors.Join(handleErr, closeErr, fdExitErr)

		close(u.readyCh)
		close(u.exitCh)
	}()

	return nil
}

func (u *Uffd) handle(sandboxId string) error {
	err := u.lis.SetDeadline(time.Now().Add(uffdMsgListenerTimeout))
	if err != nil {
		return fmt.Errorf("failed setting listener deadline: %w", err)
	}

	conn, err := u.lis.Accept()
	if err != nil {
		return fmt.Errorf("failed accepting firecracker connection: %w", err)
	}

	unixConn := conn.(*net.UnixConn)

	mappingsBuf := make([]byte, mappingsSize)
	uffdBuf := make([]byte, syscall.CmsgSpace(fdSize))

	numBytesMappings, numBytesFd, _, _, err := unixConn.ReadMsgUnix(mappingsBuf, uffdBuf)
	if err != nil {
		return fmt.Errorf("failed to read unix msg from connection: %w", err)
	}

	mappingsBuf = mappingsBuf[:numBytesMappings]

	var m mapping.FcMappings

	err = json.Unmarshal(mappingsBuf, &m)
	if err != nil {
		return fmt.Errorf("failed parsing memory mapping data: %w", err)
	}

	controlMsgs, err := syscall.ParseSocketControlMessage(uffdBuf[:numBytesFd])
	if err != nil {
		return fmt.Errorf("failed parsing control messages: %w", err)
	}

	if len(controlMsgs) != 1 {
		return fmt.Errorf("expected 1 control message containing UFFD: found %d", len(controlMsgs))
	}

	fds, err := syscall.ParseUnixRights(&controlMsgs[0])
	if err != nil {
		return fmt.Errorf("failed parsing unix write: %w", err)
	}

	if len(fds) != 1 {
		return fmt.Errorf("expected 1 fd: found %d", len(fds))
	}

	uffd := userfaultfd.NewUserfaultfdFromFd(uintptr(fds[0]))

	defer func() {
		closeErr := uffd.Close()
		if closeErr != nil {
			zap.L().Error("failed to close uffd", logger.WithSandboxID(sandboxId), zap.String("socket_path", u.socketPath), zap.Error(closeErr))
		}
	}()

	// if u.writeProtection {
	// 	for _, region := range m {
	// 		// Register the WP. It is possible that the memory region was already registered (with missing pages in FC), but registering it again with bigger subset should merge these.
	// 		err := uffd.Register(
	// 			region.Offset+region.BaseHostVirtAddr,
	// 			uint64(region.Size),
	// 			userfaultfd.UFFDIO_REGISTER_MODE_WP|userfaultfd.UFFDIO_REGISTER_MODE_MISSING,
	// 		)
	// 		if err != nil {
	// 			return fmt.Errorf("failed to reregister memory region with write protection %d-%d", region.Offset, region.Offset+region.Size)
	// 		}

	// 		Add write protection to the regions provided by the UFFD
	// 		err = uffd.AddWriteProtection(
	// 			region.Offset+region.BaseHostVirtAddr,
	// 			uint64(region.Size),
	// 		)
	// 		if err != nil {
	// 			return fmt.Errorf("failed to add write protection to region %d-%d", region.Offset, region.Offset+region.Size)
	// 		}
	// 	}
	// }

	fmt.Fprintf(os.Stderr, "uffd: serving: %d\n", len(m))

	u.readyCh <- struct{}{}

	err = uffd.Serve(
		m,
		u.memfile,
		u.fdExit,
		zap.L().With(logger.WithSandboxID(sandboxId)),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve error %v", err)
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}

func (u *Uffd) Stop() error {
	return u.fdExit.SignalExit()
}

func (u *Uffd) Ready() chan struct{} {
	return u.readyCh
}

func (u *Uffd) Exit() chan error {
	return u.exitCh
}

func (u *Uffd) TrackAndReturnNil() error {
	return u.lis.Close()
}

func (u *Uffd) Disable() error {
	return u.memfile.Disable()
}

func (u *Uffd) Dirty() *bitset.BitSet {
	return u.memfile.Dirty()
}
