package nfsproxy

import (
	"context"
	"fmt"
	"net"

	"github.com/willscott/go-nfs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy")

func newSpanHook() nfs.Hook {
	return nfs.Hook{
		OnConnect: func(ctx context.Context, conn net.Conn) (context.Context, net.Conn) {
			ctx, span := tracer.Start(ctx, "start nfs proxy server connection") //nolint:spancheck // called by OnDisconnect

			conn = &connWithSpan{Conn: conn, span: span}

			return ctx, conn //nolint:spancheck // called by OnDisconnect
		},
		OnDisconnect: func(ctx context.Context, conn net.Conn) {
			cws, ok := conn.(*connWithSpan)
			if !ok {
				logger.L().Warn(ctx, "failed to unwrap connWithSpan",
					zap.String("conn_type", fmt.Sprintf("%T", conn)))

				return
			}

			cws.span.End()
		},
	}
}

type connWithSpan struct {
	net.Conn

	span trace.Span
}
