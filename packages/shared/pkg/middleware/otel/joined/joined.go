package joined

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AttributeKey is the dotted-lowercase key used for both the histogram
// attribute and the span attribute.
const AttributeKey = "request.joined"

type holder struct {
	joined atomic.Bool

	serverSpan trace.Span
}

type holderKey struct{}

// WithHolder installs a fresh holder on ctx if one is not already present.
//
// Call WithHolder *after* the server span has been started so the holder
// captures the correct span for Mark to write attributes onto.
func WithHolder(ctx context.Context) context.Context {
	if _, ok := ctx.Value(holderKey{}).(*holder); ok {
		return ctx
	}

	return context.WithValue(ctx, holderKey{}, &holder{
		serverSpan: trace.SpanFromContext(ctx),
	})
}

// Mark marks the current request as a joiner. First-write-wins
func Mark(ctx context.Context) {
	h, ok := ctx.Value(holderKey{}).(*holder)
	if !ok {
		return
	}

	if h.joined.CompareAndSwap(false, true) {
		h.serverSpan.SetAttributes(
			attribute.Bool(AttributeKey, true),
		)
	}
}

func Attribute(ctx context.Context) attribute.KeyValue {
	h, ok := ctx.Value(holderKey{}).(*holder)
	if !ok {
		return attribute.Bool(AttributeKey, false)
	}

	return attribute.Bool(AttributeKey, h.joined.Load())
}
