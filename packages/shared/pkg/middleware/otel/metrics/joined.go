package metrics

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const requestJoinedAttrKey = "request.joined"

type joinedHolder struct {
	joined atomic.Bool
	// serverSpan is captured at middleware entry so MarkJoined can pin the
	// request.joined attribute onto the top-level HTTP server span instead
	// of whatever child span (e.g. "create-sandbox") happens to be active
	// when the helper is called. Tracing middleware is registered before
	// the metrics middleware in every service that mounts both, so the ctx
	// passed to withJoinedHolder carries the server span as the active span.
	// If no real span is on the ctx, this is a no-op span and SetAttributes
	// silently does nothing.
	serverSpan trace.Span
}

type joinedHolderKey struct{}

// withJoinedHolder installs a fresh joinedHolder on ctx and returns the
// augmented context plus a handle to the holder. The metrics middleware
// calls this once per request; non-HTTP callers normally never call it,
// which leaves the package helpers as safe no-ops.
func withJoinedHolder(ctx context.Context) (context.Context, *joinedHolder) {
	h := &joinedHolder{
		serverSpan: trace.SpanFromContext(ctx),
	}

	return context.WithValue(ctx, joinedHolderKey{}, h), h
}

// MarkJoined marks the current request as having joined an in-flight
// concurrent operation (e.g. waiting for another request to finish a sandbox
// state transition, or joining a concurrent CreateSandbox). The flag is
// emitted as a histogram attribute on http.server.duration and as an
// attribute on the top-level HTTP server span (first-write-wins).
//
// Safe to call from any goroutine descended from the request context.
// No-op if ctx has no joinedHolder (e.g. non-HTTP callers, tests).
func MarkJoined(ctx context.Context) {
	h, ok := ctx.Value(joinedHolderKey{}).(*joinedHolder)
	if !ok {
		return
	}

	if h.joined.CompareAndSwap(false, true) {
		h.serverSpan.SetAttributes(
			attribute.String(requestJoinedAttrKey, "true"),
		)
	}
}

func (h *joinedHolder) joinedAttribute() attribute.KeyValue {
	return attribute.Bool(requestJoinedAttrKey, h.joined.Load())
}
