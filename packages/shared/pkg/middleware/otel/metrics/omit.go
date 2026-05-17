package metrics

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const requestJoinedAttrKey = "request.joined"

type omitHolder struct {
	joined atomic.Bool
}

type omitHolderKey struct{}

// WithOmitHolder installs a fresh omitHolder on ctx and returns the augmented
// context plus a handle to the holder. The metrics middleware calls this
// once per request; non-HTTP callers normally never call it, which leaves
// the package helpers as safe no-ops.
func WithOmitHolder(ctx context.Context) (context.Context, *omitHolder) {
	h := &omitHolder{}
	return context.WithValue(ctx, omitHolderKey{}, h), h
}

// MarkJoined marks the current request as having joined an in-flight
// concurrent operation (e.g. waiting for another request to finish a sandbox
// state transition, or joining a concurrent CreateSandbox). The flag is
// emitted as a histogram attribute on http.server.duration and as a span
// attribute on the active trace span (first-write-wins).
//
// Safe to call from any goroutine descended from the request context.
// No-op if ctx has no omitHolder (e.g. non-HTTP callers, tests).
func MarkJoined(ctx context.Context) {
	h, ok := ctx.Value(omitHolderKey{}).(*omitHolder)
	if !ok {
		return
	}

	if h.joined.CompareAndSwap(false, true) {
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String(requestJoinedAttrKey, "true"),
		)
	}
}

func (h *omitHolder) joinedAttribute() attribute.KeyValue {
	return attribute.Bool(requestJoinedAttrKey, h.joined.Load())
}
