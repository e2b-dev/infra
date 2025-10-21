package uffd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd")

const (
	uffdMsgListenerTimeout = 10 * time.Second
	fdSize                 = 4
	regionMappingsSize     = 1024
)

type Uffd struct {
	exit       *utils.ErrorOnce
	readyCh    chan struct{}
	fdExit     *fdexit.FdExit
	lis        *net.UnixListener
	socketPath string
	memfile    block.ReadonlyDevice
	handler    utils.SetOnce[*userfaultfd.Userfaultfd]
}

var _ MemoryBackend = (*Uffd)(nil)

func New(memfile block.ReadonlyDevice, socketPath string) (*Uffd, error) {
	fdExit, err := fdexit.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create fd exit: %w", err)
	}

	return &Uffd{
		exit:       utils.NewErrorOnce(),
		readyCh:    make(chan struct{}, 1),
		fdExit:     fdExit,
		socketPath: socketPath,
		memfile:    memfile,
		handler:    *utils.NewSetOnce[*userfaultfd.Userfaultfd](),
	}, nil
}

func (u *Uffd) Start(ctx context.Context, sandboxId string) error {
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
		ctx, span := tracer.Start(ctx, "serve uffd")
		defer span.End()

		// TODO: If the handle function fails, we should kill the sandbox
		handleErr := u.handle(ctx, sandboxId)
		closeErr := u.lis.Close()
		fdExitErr := u.fdExit.Close()

		u.exit.SetError(errors.Join(handleErr, closeErr, fdExitErr))

		close(u.readyCh)
	}()

	return nil
}

func (u *Uffd) handle(ctx context.Context, sandboxId string) error {
	err := u.lis.SetDeadline(time.Now().Add(uffdMsgListenerTimeout))
	if err != nil {
		return fmt.Errorf("failed setting listener deadline: %w", err)
	}

	conn, err := u.lis.Accept()
	if err != nil {
		return fmt.Errorf("failed accepting firecracker connection: %w", err)
	}

	unixConn := conn.(*net.UnixConn)

	regionMappingsBuf := make([]byte, regionMappingsSize)
	uffdBuf := make([]byte, syscall.CmsgSpace(fdSize))

	numBytesMappings, numBytesFd, _, _, err := unixConn.ReadMsgUnix(regionMappingsBuf, uffdBuf)
	if err != nil {
		return fmt.Errorf("failed to read unix msg from connection: %w", err)
	}

	regionMappingsBuf = regionMappingsBuf[:numBytesMappings]

	var regions []memory.Region

	err = json.Unmarshal(regionMappingsBuf, &regions)
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

	m := memory.NewMapping(regions)

	uffd, err := userfaultfd.NewUserfaultfdFromFd(
		uintptr(fds[0]),
		u.memfile,
		u.memfile.BlockSize(),
		m,
		zap.L().With(logger.WithSandboxID(sandboxId)),
	)
	if err != nil {
		return fmt.Errorf("failed to create uffd: %w", err)
	}

	u.handler.SetValue(uffd)

	defer func() {
		closeErr := uffd.Close()
		if closeErr != nil {
			zap.L().Error("failed to close uffd", logger.WithSandboxID(sandboxId), zap.String("socket_path", u.socketPath), zap.Error(closeErr))
		}
	}()

	for _, region := range m.Regions {
		// Register the WP. It is possible that the memory region was already registered (with missing pages in FC), but registering it again with bigger flag subset should merge these.
		// - https://github.com/firecracker-microvm/firecracker/blob/f335a0adf46f0680a141eb1e76fe31ac258918c5/src/vmm/src/persist.rs#L477
		// - https://github.com/bytecodealliance/userfaultfd-rs/blob/main/src/builder.rs
		err := uffd.Register(
			region.BaseHostVirtAddr+region.Offset,
			uint64(region.Size),
			userfaultfd.UFFDIO_REGISTER_MODE_WP|userfaultfd.UFFDIO_REGISTER_MODE_MISSING,
		)
		if err != nil {
			return fmt.Errorf("failed to reregister memory region with write protection %d-%d", region.Offset, region.Offset+region.Size)
		}
	}

	u.readyCh <- struct{}{}

	err = uffd.Serve(
		ctx,
		u.fdExit,
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

func (u *Uffd) Disable(ctx context.Context) error {
	uffd, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get uffd: %w", err)
	}

	uffd.Disable()

	return nil
}

func (u *Uffd) Exit() *utils.ErrorOnce {
	return u.exit
}

// Dirty waits for all the requests in flight to be finished and then returns clone of the dirty tracker.
// Call *after* pausing the firecracker process—to let the uffd process all the requests.
func (u *Uffd) Dirty(ctx context.Context) (*block.Tracker, error) {
	uffd, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get uffd: %w", err)
	}

	return uffd.Dirty(ctx)
}
