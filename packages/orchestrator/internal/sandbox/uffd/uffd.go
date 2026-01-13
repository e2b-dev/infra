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

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
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
	readyOnce  sync.Once
	lis        *net.UnixListener
	socketPath string
	memfile    block.ReadonlyDevice
	handler    utils.SetOnce[*userfaultfd.Userfaultfd]
	fdExit     utils.SetOnce[*fdexit.FdExit]
}

var _ MemoryBackend = (*Uffd)(nil)

func New(memfile block.ReadonlyDevice, socketPath string) *Uffd {
	return &Uffd{
		exit:       utils.NewErrorOnce(),
		readyCh:    make(chan struct{}),
		socketPath: socketPath,
		memfile:    memfile,
		handler:    *utils.NewSetOnce[*userfaultfd.Userfaultfd](),
		fdExit:     *utils.NewSetOnce[*fdexit.FdExit](),
	}
}

func (u *Uffd) Prefault(ctx context.Context, offset int64, data []byte) error {
	handler, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get uffd: %w", err)
	}

	return handler.Prefault(ctx, offset, data)
}

func (u *Uffd) Start(ctx context.Context, sandboxId string) error {
	lis, err := net.ListenUnix("unix", &net.UnixAddr{Name: u.socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("failed listening on socket: %w", err)
	}

	u.lis = lis

	err = os.Chmod(u.socketPath, 0o777)
	if err != nil {
		closeErr := lis.Close()

		return fmt.Errorf("failed setting socket permissions: %w", errors.Join(err, closeErr))
	}

	fdExit, err := fdexit.New()
	if err != nil {
		closeErr := lis.Close()

		return fmt.Errorf("failed to create fd exit: %w", errors.Join(err, closeErr))
	}

	u.fdExit.SetValue(fdExit)

	go func() {
		ctx, span := tracer.Start(ctx, "serve uffd")
		defer span.End()

		// TODO: If the handle function fails, we should kill the sandbox
		handleErr := u.handle(ctx, sandboxId, fdExit)

		// If handle failed before setting the handler value, set an error to unblock
		// any waiters (e.g., prefetcher goroutines waiting on Prefault).
		if handleErr != nil {
			u.handler.SetError(handleErr)
		}

		closeErr := u.lis.Close()
		fdExitErr := fdExit.Close()

		u.exit.SetError(errors.Join(handleErr, closeErr, fdExitErr))

		// Close the ready channel to unblock any waiters (safe to call multiple times via Once)
		u.readyOnce.Do(func() { close(u.readyCh) })
	}()

	return nil
}

func (u *Uffd) handle(ctx context.Context, sandboxId string, fdExit *fdexit.FdExit) error {
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
		m,
		logger.L().With(logger.WithSandboxID(sandboxId)),
	)
	if err != nil {
		return fmt.Errorf("failed to create uffd: %w", err)
	}

	u.handler.SetValue(uffd)

	defer func() {
		closeErr := uffd.Close()
		if closeErr != nil {
			logger.L().Error(ctx, "failed to close uffd", logger.WithSandboxID(sandboxId), zap.String("socket_path", u.socketPath), zap.Error(closeErr))
		}
	}()

	u.readyOnce.Do(func() { close(u.readyCh) })

	err = uffd.Serve(
		ctx,
		fdExit,
	)
	if err != nil {
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}

func (u *Uffd) Stop() error {
	fdExit, err := u.fdExit.Result()
	if err != nil {
		return fmt.Errorf("fdExit not set or failed: %w", err)
	}

	return fdExit.SignalExit()
}

func (u *Uffd) Ready() chan struct{} {
	return u.readyCh
}

func (u *Uffd) Exit() *utils.ErrorOnce {
	return u.exit
}

// DiffMetadata waits for the current requests to finish and returns the dirty pages.
//
// It *MUST* be only called after the sandbox was successfully paused via API and after the snapshot endpoint was called.
func (u *Uffd) DiffMetadata(ctx context.Context) (*header.DiffMetadata, error) {
	uffd, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get uffd: %w", err)
	}

	return &header.DiffMetadata{
		Dirty: uffd.Dirty().BitSet(),
		// We don't track and filter empty pages for subsequent sandbox pauses as pages should usually not be empty.
		Empty:     bitset.New(0),
		BlockSize: u.memfile.BlockSize(),
	}, nil
}

// PrefetchData returns page fault data for prefetch mapping.
func (u *Uffd) PrefetchData(ctx context.Context) (block.PrefetchData, error) {
	uffd, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return block.PrefetchData{}, fmt.Errorf("failed to get uffd: %w", err)
	}

	return uffd.PrefetchData(), nil
}
