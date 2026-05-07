package uffd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd")

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
	memfd      atomic.Pointer[block.Memfd]
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
	// Firecracker might send us up to 2 file descriptors. Older Firecracker versions will just
	// send us the UFFD file descriptor. Newer ones will also send the memfd used to back the
	// guest memory
	fdBuf := make([]byte, syscall.CmsgSpace(2*fdSize))

	numBytesMappings, numBytesFd, _, _, err := unixConn.ReadMsgUnix(regionMappingsBuf, fdBuf)
	if err != nil {
		return fmt.Errorf("failed to read unix msg from connection: %w", err)
	}

	regionMappingsBuf = regionMappingsBuf[:numBytesMappings]

	var regions []memory.Region

	err = json.Unmarshal(regionMappingsBuf, &regions)
	if err != nil {
		return fmt.Errorf("failed parsing memory mapping data: %w", err)
	}

	controlMsgs, err := syscall.ParseSocketControlMessage(fdBuf[:numBytesFd])
	if err != nil {
		return fmt.Errorf("failed parsing control messages: %w", err)
	}

	if len(controlMsgs) != 1 {
		return fmt.Errorf("expected 1 control message containing UFFD and (maybe) memfd: found %d", len(controlMsgs))
	}

	fds, err := syscall.ParseUnixRights(&controlMsgs[0])
	if err != nil {
		return fmt.Errorf("failed parsing unix write: %w", err)
	}

	if len(fds) == 0 {
		return errors.New("expected at least 1 file descriptor")
	}

	m := memory.NewMapping(regions)

	uffd, err := userfaultfd.NewUserfaultfdFromFd(
		uintptr(fds[0]),
		u.memfile,
		m,
		logger.L().With(logger.WithSandboxID(sandboxId)),
	)
	if err != nil {
		syscall.Close(fds[0])
		if len(fds) > 1 {
			syscall.Close(fds[1])
		}

		return fmt.Errorf("failed to create uffd: %w", err)
	}

	defer func() {
		closeErr := uffd.Close()
		if closeErr != nil {
			logger.L().Error(ctx, "failed to close uffd", logger.WithSandboxID(sandboxId), zap.String("socket_path", u.socketPath), zap.Error(closeErr))
		}

		if m := u.memfd.Swap(nil); m != nil {
			if closeErr := m.Close(); closeErr != nil {
				logger.L().Error(ctx, "failed to close memfd", logger.WithSandboxID(sandboxId), zap.Error(closeErr))
			}
		}
	}()

	var memfd *block.Memfd
	if len(fds) > 1 {
		memfd = block.NewFromFd(fds[1])
		u.memfd.Store(memfd)
	}

	u.handler.SetValue(uffd)

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
func (u *Uffd) DiffMetadata(ctx context.Context, f *fc.Process) (*header.DiffMetadata, error) {
	return f.DirtyMemory(ctx, u.memfile.BlockSize())
}

// PrefetchData returns page fault data for prefetch mapping.
func (u *Uffd) PrefetchData(ctx context.Context) (block.PrefetchData, error) {
	uffd, err := u.handler.WaitWithContext(ctx)
	if err != nil {
		return block.PrefetchData{}, fmt.Errorf("failed to get uffd: %w", err)
	}

	return uffd.PrefetchData(), nil
}

// Memfd returns the memfd received from Firecracker and transfers ownership to
// the caller. The uffd teardown defer will no longer close it.
func (u *Uffd) Memfd(_ context.Context) *block.Memfd {
	return u.memfd.Swap(nil)
}
