package sandbox

import (
	"context"
	"fmt"
	"log"
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
	uffdSocketPath string
	memfilePath    string

	file *os.File
	conn *net.UnixConn
}

func (u *uffd) start() error {
	file, err := os.OpenFile(u.memfilePath, os.O_RDONLY, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to open memfile: %w", err)
	}
	defer func() {
		if err != nil {
			file.Close()
		}
	}()

	u.file = file

	addr, err := net.ResolveUnixAddr("unix", u.uffdSocketPath)
	if err != nil {
		return fmt.Errorf("failed to resolve unix addr: %w", err)
	}

	lis, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("failed to listen unix: %w", err)
	}

	for {
		conn, err := lis.AcceptUnix()

		u.conn = conn

		if err != nil {
			return fmt.Errorf("failed to accept unix conn: %w", err)
		}

		go func() {
			defer func() {
				if err := recover(); err != nil {
					log.Println("Could not handle connection, stopping:", err)
				}

				_ = conn.Close()
			}()

			ud, start, err := transfer.ReceiveUFFD(conn)
			if err != nil {
				fmt.Printf("failed to receive uffd: %v", err)
				return
			}

			if err := handleUffd(ud, start, u.file, hugePageSize); err != nil {
				fmt.Printf("failed to handle uffd: %v", err)
				return
			}
		}()
	}
}

func (u *uffd) stop(ctx context.Context, tracer trace.Tracer) {
	childCtx, childSpan := tracer.Start(ctx, "stop-uffd", trace.WithAttributes())
	defer childSpan.End()

	if u.file != nil {
		err := u.file.Close()
		if err != nil {
			errMsg := fmt.Errorf("failed to close memfile: %w", err)
			telemetry.ReportError(childCtx, errMsg)
		}
	}

	if u.conn != nil {
		err := u.conn.Close()
		if err != nil {
			errMsg := fmt.Errorf("failed to close unix conn: %w", err)
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
