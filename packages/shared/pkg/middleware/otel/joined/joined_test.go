package joined_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/e2b-dev/infra/packages/shared/pkg/middleware/otel/joined"
)

// Mark must be safe even when the context carries no holder.
func TestMark_NoHolder_Noop(t *testing.T) {
	t.Parallel()
	joined.Mark(context.Background())
}

// Attribute must return request.joined=false when no holder is on ctx.
func TestAttribute_NoHolder_ReturnsFalse(t *testing.T) {
	t.Parallel()

	a := joined.Attribute(context.Background())
	assert.Equal(t, joined.AttributeKey, string(a.Key))
	assert.False(t, a.Value.AsBool())
}

// Attribute must return request.joined=false on a freshly installed holder
// before Mark has been called.
func TestAttribute_FreshHolder_ReturnsFalse(t *testing.T) {
	t.Parallel()

	ctx := joined.WithHolder(context.Background())

	a := joined.Attribute(ctx)
	assert.False(t, a.Value.AsBool())
}

// Mark must flip Attribute to true on the same ctx.
func TestMark_FlipsAttributeToTrue(t *testing.T) {
	t.Parallel()

	ctx := joined.WithHolder(context.Background())
	joined.Mark(ctx)

	a := joined.Attribute(ctx)
	assert.True(t, a.Value.AsBool())
}

// WithHolder must be idempotent: calling it twice returns a ctx that shares
// the same underlying holder (Mark on the first ctx is visible from the
// second).
func TestWithHolder_Idempotent(t *testing.T) {
	t.Parallel()

	ctx1 := joined.WithHolder(context.Background())
	ctx2 := joined.WithHolder(ctx1)

	joined.Mark(ctx1)

	a := joined.Attribute(ctx2)
	assert.True(t, a.Value.AsBool(), "second WithHolder must reuse the first holder")
}

// Mark must pin request.joined="true" onto the server span captured at
// WithHolder install time, not onto whichever child span is active when
// Mark fires.
func TestMark_TagsServerSpanNotChildSpan(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	tracer := tp.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/middleware/otel/joined")

	// Open the server span before installing the holder (mirrors tracing
	// middleware ordering: tracer.Start -> WithHolder).
	ctx, serverSpan := tracer.Start(context.Background(), "HTTP POST /resume")
	ctx = joined.WithHolder(ctx)

	// Open a child span (mirrors orchestrator.CreateSandbox's
	// "create-sandbox" child) and call Mark from inside it.
	childCtx, childSpan := tracer.Start(ctx, "create-sandbox")
	joined.Mark(childCtx)
	childSpan.End()
	serverSpan.End()

	spans := sr.Ended()
	require.Len(t, spans, 2)

	var server, child sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == "create-sandbox" {
			child = s
		} else {
			server = s
		}
	}
	require.NotNil(t, server)
	require.NotNil(t, child)

	serverAttr, hasServer := findAttr(server.Attributes(), joined.AttributeKey)
	require.True(t, hasServer, "request.joined must be on the server span")
	assert.Equal(t, "true", serverAttr.AsString())

	_, hasChild := findAttr(child.Attributes(), joined.AttributeKey)
	assert.False(t, hasChild, "request.joined must NOT be on the child span")
}

// Mark must be safe when called from a goroutine descended from the
// request context.
func TestMark_DescendantGoroutine(t *testing.T) {
	t.Parallel()

	ctx := joined.WithHolder(context.Background())
	done := make(chan struct{})
	go func() {
		joined.Mark(ctx)
		close(done)
	}()
	<-done

	a := joined.Attribute(ctx)
	assert.True(t, a.Value.AsBool())
}

func findAttr(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value, true
		}
	}

	return attribute.Value{}, false
}
