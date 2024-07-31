package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"

	"github.com/loopholelabs/userfaultfd-go/pkg/transfer"
	"go.opentelemetry.io/otel/trace"
)

const (
	hugePageSize = 2 * 1024 * 1024 // 2 MB
)

type uffd struct {
	memfileHandle *os.File
	socketCancel  context.CancelFunc

	uffdSocketPath string
	memfilePath    string
}

func (u *uffd) start(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "start-uffd", trace.WithAttributes())
	defer childSpan.End()

	file, err := os.OpenFile(u.memfilePath, os.O_RDONLY, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to open memfile: %w", err)
	}

	u.memfileHandle = file

	addr, err := net.ResolveUnixAddr("unix", u.uffdSocketPath)
	if err != nil {
		return fmt.Errorf("failed to resolve unix addr: %w", err)
	}

	telemetry.ReportEvent(ctx, "resolved unix socket")

	lis, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("failed to listen unix: %w", err)
	}

	telemetry.ReportEvent(ctx, "created unix socket listener")

	socketCtx, socketCancel := context.WithCancel(context.Background())
	u.socketCancel = socketCancel

	go func() {
		conn, lisErr := lis.AcceptUnix()
		if lisErr != nil {
			fmt.Printf("failed to accept unix conn: %v\n", lisErr)
			return
		}

		telemetry.ReportEvent(childCtx, "accepted unix conn")

		defer func() {
			if err := recover(); err != nil {
				fmt.Println("Could not handle connection, stopping:", err)
			}

			_ = conn.Close()
		}()

		ud, start, receiveUffdErr := transfer.ReceiveUFFD(conn)
		if receiveUffdErr != nil {
			fmt.Printf("failed to receive uffd: %+v\n", receiveUffdErr)
			return
		}

		if uffdErr := handleUffd(socketCtx, ud, start, u.memfileHandle, hugePageSize); uffdErr != nil {
			fmt.Printf("failed to handle uffd: %+v\n", uffdErr)
			return
		}
	}()

	return nil
}

func (u *uffd) stop(ctx context.Context, tracer trace.Tracer) {
	childCtx, childSpan := tracer.Start(ctx, "stop-uffd", trace.WithAttributes())
	defer childSpan.End()

	if u.socketCancel != nil {
		u.socketCancel()
	}

	if u.memfileHandle != nil {
		err := u.memfileHandle.Close()
		if err != nil {
			errMsg := fmt.Errorf("failed to close memfile: %w", err)
			telemetry.ReportError(childCtx, errMsg)
		}
	}
}

func newUFFD(fsEnv *SandboxFiles) *uffd {
	memfilePath := filepath.Join(fsEnv.EnvPath, MemfileName)

	return &uffd{
		memfilePath:    memfilePath,
		uffdSocketPath: *fsEnv.UFFDSocketPath,
	}
}
