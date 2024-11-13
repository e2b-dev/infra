package uffd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	template "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	uffdMsgListenerTimeout = 15 * time.Second
	fdSize                 = 4
	mappingsSize           = 1024
)

type UffdSetup struct {
	Mappings []GuestRegionUffdMapping
	Fd       uintptr
}

func New(
	memfile *template.BlockStorage,
	socketPath,
	envID,
	buildID string,
) (*Uffd, error) {
	pRead, pWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create exit fd: %w", err)
	}

	return &Uffd{
		exitChan:   make(chan error, 1),
		PollReady:  make(chan struct{}, 1),
		exitReader: pRead,
		exitWriter: pWrite,
		envID:      envID,
		buildID:    buildID,
		memfile:    memfile,
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

type Uffd struct {
	exitChan  chan error
	PollReady chan struct{}

	exitReader *os.File
	exitWriter *os.File

	Stop func() error

	lis *net.UnixListener

	socketPath string
	memfile    *template.BlockStorage

	envID   string
	buildID string
}

func (u *Uffd) Start(
	ctx context.Context,
	tracer trace.Tracer,
	logger *logs.SandboxLogger,
) error {
	childCtx, childSpan := tracer.Start(ctx, "start-uffd")
	defer childSpan.End()

	lis, err := net.ListenUnix("unix", &net.UnixAddr{Name: u.socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("failed listening on socket: %w", err)
	}

	u.lis = lis

	telemetry.ReportEvent(childCtx, "listening on socket")

	err = os.Chmod(u.socketPath, 0o777)
	if err != nil {
		return fmt.Errorf("failed setting socket permissions: %w", err)
	}

	telemetry.ReportEvent(childCtx, "set socket permissions")

	go func() {
		u.exitChan <- u.handle(logger)
		close(u.exitChan)
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

func (u *Uffd) handle(logger *logs.SandboxLogger) (err error) {
	setup, err := u.receiveSetup()
	if err != nil {
		return fmt.Errorf("failed to receive setup message from firecracker: %w", err)
	}

	uffd := setup.Fd
	defer func() {
		closeErr := syscall.Close(int(uffd))
		if closeErr != nil {
			logger.Errorf("failed to close uffd: %v", closeErr)
		}
	}()

	u.PollReady <- struct{}{}

	err = Serve(int(uffd), setup.Mappings, u.memfile, u.exitReader.Fd())
	if err != nil {
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}

func (u *Uffd) Wait() error {
	handleErr := <-u.exitChan

	close(u.PollReady)

	closeErr := u.lis.Close()
	writerErr := u.exitWriter.Close()

	return errors.Join(handleErr, closeErr, writerErr)
}
