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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

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
	exitCh  chan error
	readyCh chan struct{}

	fdExit *fdexit.FdExit

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
	trackedMemfile, err := block.NewTrackedSliceDevice(blockSize, memfile)
	if err != nil {
		return nil, fmt.Errorf("failed to create tracked slice device: %w", err)
	}

	fdExit, err := fdexit.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create fd exit: %w", err)
	}

	return &Uffd{
		exitCh:     make(chan error, 1),
		readyCh:    make(chan struct{}, 1),
		fdExit:     fdExit,
		memfile:    trackedMemfile,
		socketPath: socketPath,
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
			zap.L().Error("failed to close uffd", logger.WithSandboxID(sandboxId), zap.String("socket_path", u.socketPath), zap.Error(closeErr))
		}
	}()

	u.readyCh <- struct{}{}

	err = Serve(
		int(uffd),
		setup.Mappings,
		u.memfile,
		u.fdExit,
		sandboxId,
	)
	if err != nil {
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
