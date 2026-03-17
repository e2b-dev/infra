package tracing

import (
	"context"
	"net"

	"go.opentelemetry.io/otel/trace"
)

func OnConnect(ctx context.Context, _ net.Conn) context.Context {
	ctx, _ = tracer.Start(ctx, "start nfs proxy server connection") //nolint:spancheck // called by OnDisconnect

	return ctx
}

func OnDisconnect(ctx context.Context, _ net.Conn) {
	span := trace.SpanFromContext(ctx)

	span.End()
}
