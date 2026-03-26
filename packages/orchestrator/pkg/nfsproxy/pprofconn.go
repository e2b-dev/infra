package nfsproxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"

	"github.com/willscott/go-nfs"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func newPprofHook(outputDir string) nfs.Hook {
	return nfs.Hook{
		OnConnect: func(ctx context.Context, conn net.Conn) (context.Context, net.Conn) {
			timestamp := time.Now().Format("20060102-150405")
			filename := fmt.Sprintf("nfsproxy-cpu-%s-%s.pprof", timestamp, conn.RemoteAddr().String())
			filename = filepath.Join(outputDir, filename)

			f, err := os.Create(filename)
			if err != nil {
				logger.L().Error(ctx, "failed to create pprof file",
					zap.String("filename", filename),
					zap.Error(err))

				return ctx, conn
			}

			if err := pprof.StartCPUProfile(f); err != nil {
				logger.L().Error(ctx, "failed to start CPU profile",
					zap.String("filename", filename),
					zap.Error(err))
				f.Close()

				return ctx, conn
			}

			logger.L().Info(ctx, "started CPU profile",
				zap.String("filename", filename),
				zap.String("remote_addr", conn.RemoteAddr().String()))

			conn = &connWithPprof{Conn: conn, file: f, filename: filename}

			return ctx, conn
		},
		OnDisconnect: func(ctx context.Context, conn net.Conn) {
			cwp, ok := conn.(*connWithPprof)
			if !ok {
				logger.L().Warn(ctx, "failed to unwrap connWithPprof",
					zap.String("conn_type", fmt.Sprintf("%T", conn)))

				return
			}

			pprof.StopCPUProfile()

			if err := cwp.file.Close(); err != nil {
				logger.L().Error(ctx, "failed to close pprof file",
					zap.String("filename", cwp.filename),
					zap.Error(err))

				return
			}

			logger.L().Info(ctx, "stopped CPU profile and wrote to disk",
				zap.String("filename", cwp.filename))
		},
	}
}

type connWithPprof struct {
	net.Conn

	file     *os.File
	filename string
}
