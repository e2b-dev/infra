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

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"go.uber.org/zap"

	"github.com/bits-and-blooms/bitset"
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
	Exit  chan error
	Ready chan struct{}

	exitReader *os.File
	exitWriter *os.File

	Stop func() error

	lis *net.UnixListener

	memfile    *block.TrackedSliceDevice
	socketPath string
	clientID   string
}

func (u *Uffd) Disable() error {
	return u.memfile.Disable()
}

func (u *Uffd) Dirty() *bitset.BitSet {
	return u.memfile.Dirty()
}

func New(memfile block.ReadonlyDevice, socketPath string, blockSize int64, clientID string) (*Uffd, error) {
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
		clientID: clientID,
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

	err = Serve(
		int(uffd),
		setup.Mappings,
		u.memfile,
		u.exitReader.Fd(),
		u.Stop,
		sandboxId,
		u.clientID,
	)
	if err != nil {
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}
