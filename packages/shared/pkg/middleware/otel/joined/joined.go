// Package joined provides a request-scoped, concurrency-safe marker for
// "this HTTP request joined an in-flight concurrent operation rather than
// doing fresh work" (e.g. waiting for another request to finish a sandbox
// state transition, or piggy-backing on a concurrent CreateSandbox).
//
// The marker is installed once per request by either the tracing or the
// metrics middleware (both call WithHolder, which is idempotent). Any code
// path descended from the request context can flip the marker via Mark,
// without needing access to *gin.Context.
//
//   - The marker writes request.joined="true" to the top-level HTTP server
//     span captured at install time (so the attribute always lands on the
//     root span, regardless of which child span is active when Mark fires).
//   - The marker is exposed as a boolean histogram attribute via Attribute,
//     so the metrics middleware can emit request.joined=true/false on every
//     observation and dashboards can filter joiner vs. normal traffic.
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
	// serverSpan is captured at holder install time. It is the span that is
	// active on the ctx when WithHolder is called. In services that mount
	// the tracing middleware before the metrics middleware (the convention
	// in this repo), this is the HTTP server span.
	serverSpan trace.Span
}

type holderKey struct{}

// WithHolder installs a fresh holder on ctx if one is not already present.
// Idempotent: when both the tracing and the metrics middleware are mounted,
// whichever runs first installs the holder; the other reuses it.
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

// Mark marks the current request as a joiner. First-write-wins: subsequent
// calls in the same request are no-ops. Safe to call from any goroutine
// descended from the request ctx. No-op if no holder is on ctx (e.g.
// non-HTTP callers, tests).
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

// Attribute returns a boolean histogram attribute reflecting whether Mark
// has been called on this request's holder. Always returns a valid
// attribute so callers can append it unconditionally; if no holder is on
// ctx the value is false (no joiner status).
func Attribute(ctx context.Context) attribute.KeyValue {
	h, ok := ctx.Value(holderKey{}).(*holder)
	if !ok {
		return attribute.Bool(AttributeKey, false)
	}

	return attribute.Bool(AttributeKey, h.joined.Load())
}
