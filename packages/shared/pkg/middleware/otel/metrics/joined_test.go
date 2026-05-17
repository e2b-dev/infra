package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// fakeRecorder captures every ObserveHTTPRequestDuration call so tests can
// inspect the attributes the middleware would have emitted.
type fakeRecorder struct {
	mu    sync.Mutex
	calls []fakeRecorderCall
}

type fakeRecorderCall struct {
	duration time.Duration
	attrs    []attribute.KeyValue
}

func (f *fakeRecorder) ObserveHTTPRequestDuration(_ context.Context, duration time.Duration, attrs []attribute.KeyValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy attrs because the middleware reuses its slice buffer between
	// requests when the test engine handles more than one request.
	cp := make([]attribute.KeyValue, len(attrs))
	copy(cp, attrs)
	f.calls = append(f.calls, fakeRecorderCall{duration: duration, attrs: cp})
}

func (f *fakeRecorder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

func (f *fakeRecorder) attrs(i int) []attribute.KeyValue {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls[i].attrs
}

// findRequestJoined returns the value of the `request.joined` attribute from
// the given attribute slice, if present.
func findRequestJoined(attrs []attribute.KeyValue) (attribute.Value, bool) {
	for _, a := range attrs {
		if string(a.Key) == requestJoinedAttrKey {
			return a.Value, true
		}
	}

	return attribute.Value{}, false
}

func newTestEngine(t *testing.T, handler gin.HandlerFunc) (*gin.Engine, *fakeRecorder) {
	t.Helper()

	rec := &fakeRecorder{}
	r := gin.New()
	r.Use(Middleware(nil, "test", WithRecorder(rec)))
	r.POST("/sandboxes/:id/resume", handler)

	return r, rec
}

func doRequest(t *testing.T, r *gin.Engine) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/abc/resume", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// MarkJoined must be safe even when the context carries no joinedHolder
// (e.g. non-HTTP callers, tests).
func TestMarkJoined_NoHolder_Noop(t *testing.T) {
	t.Parallel()
	MarkJoined(context.Background())
}

// Untagged requests must carry request.joined=false.
func TestMiddleware_NormalRequest_JoinedAttrIsFalse(t *testing.T) {
	t.Parallel()

	r, rec := newTestEngine(t, func(c *gin.Context) { c.Status(http.StatusOK) })

	doRequest(t, r)

	require.Equal(t, 1, rec.callCount())
	v, ok := findRequestJoined(rec.attrs(0))
	require.True(t, ok, "request.joined must be present on every observation")
	assert.False(t, v.AsBool(), "untagged request must carry request.joined=false")
}

// MarkJoined from the handler's ctx must flip the attribute to true.
func TestMiddleware_MarkJoinedFromHandler_AppearsAsTrue(t *testing.T) {
	t.Parallel()

	r, rec := newTestEngine(t, func(c *gin.Context) {
		MarkJoined(c.Request.Context())
		c.Status(http.StatusOK)
	})

	doRequest(t, r)

	require.Equal(t, 1, rec.callCount())
	v, ok := findRequestJoined(rec.attrs(0))
	require.True(t, ok, "request.joined attribute must be present on the histogram")
	assert.True(t, v.AsBool())
}

// MarkJoined called from a goroutine descended from the request context
// must still flow through to the histogram. This is the key capability of
// the context-attached holder design vs. a *gin.Context-based marker.
func TestMiddleware_MarkJoinedFromDescendantGoroutine(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	r, rec := newTestEngine(t, func(c *gin.Context) {
		ctx := c.Request.Context()
		go func() {
			MarkJoined(ctx)
			close(done)
		}()
		<-done
		c.Status(http.StatusOK)
	})

	doRequest(t, r)

	require.Equal(t, 1, rec.callCount())
	v, ok := findRequestJoined(rec.attrs(0))
	require.True(t, ok)
	assert.True(t, v.AsBool())
}

// MarkJoined is idempotent: repeated calls within the same request do not
// produce duplicate histogram attributes.
func TestMiddleware_MarkJoinedIdempotent(t *testing.T) {
	t.Parallel()

	r, rec := newTestEngine(t, func(c *gin.Context) {
		MarkJoined(c.Request.Context())
		MarkJoined(c.Request.Context())
		MarkJoined(c.Request.Context())
		c.Status(http.StatusOK)
	})

	doRequest(t, r)

	require.Equal(t, 1, rec.callCount())
	attrs := rec.attrs(0)

	count := 0
	for _, a := range attrs {
		if string(a.Key) == requestJoinedAttrKey {
			count++
		}
	}
	assert.Equal(t, 1, count, "request.joined must appear exactly once even after repeated MarkJoined calls")
}

// Tagging must not suppress recording — we only add a label.
func TestMiddleware_Tagging_DoesNotSuppressRecording(t *testing.T) {
	t.Parallel()

	r, rec := newTestEngine(t, func(c *gin.Context) {
		MarkJoined(c.Request.Context())
		c.Status(http.StatusOK)
	})

	doRequest(t, r)

	assert.Equal(t, 1, rec.callCount(), "histogram must still be recorded; tagging only adds attributes")
}

// MarkJoined must pin the request.joined attribute onto the top-level HTTP
// server span (captured at middleware entry), not onto whatever child span
// happens to be active when the helper is called. This is the guarantee
// callers rely on for Tempo filtering by root-span attribute.
func TestMiddleware_MarkJoined_TagsServerSpanNotChildSpan(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	tracer := tp.Tracer("test")

	rec := &fakeRecorder{}
	r := gin.New()
	// Stand-in for the tracing middleware: open the server span before the
	// metrics middleware installs its joinedHolder. This mirrors the real
	// service wiring (tracing registered before metrics).
	r.Use(func(c *gin.Context) {
		ctx, span := tracer.Start(c.Request.Context(), "HTTP POST /sandboxes/:id/resume")
		defer span.End()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(Middleware(nil, "test", WithRecorder(rec)))
	r.POST("/sandboxes/:id/resume", func(c *gin.Context) {
		// Open a child span (mirrors orchestrator.CreateSandbox's
		// "create-sandbox" child span) and call MarkJoined from inside it.
		ctx, child := tracer.Start(c.Request.Context(), "create-sandbox")
		MarkJoined(ctx)
		child.End()

		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/sandboxes/abc/resume", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	spans := sr.Ended()
	require.Len(t, spans, 2)

	// Identify server vs child by span name.
	var serverSpan, childSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == "create-sandbox" {
			childSpan = s
		} else {
			serverSpan = s
		}
	}
	require.NotNil(t, serverSpan, "server span must be recorded")
	require.NotNil(t, childSpan, "child span must be recorded")

	serverAttr, hasServer := findRequestJoined(serverSpan.Attributes())
	require.True(t, hasServer, "request.joined must be on the server span")
	assert.Equal(t, "true", serverAttr.AsString())

	_, hasChild := findRequestJoined(childSpan.Attributes())
	assert.False(t, hasChild, "request.joined must NOT be on the child span")

	// And the histogram must still carry request.joined=true.
	require.Equal(t, 1, rec.callCount())
	histAttr, ok := findRequestJoined(rec.attrs(0))
	require.True(t, ok)
	assert.True(t, histAttr.AsBool())
}

// Two distinct requests must not share the holder: tagging one must not
// taint the other.
func TestMiddleware_HolderIsRequestScoped(t *testing.T) {
	t.Parallel()

	rec := &fakeRecorder{}
	r := gin.New()
	r.Use(Middleware(nil, "test", WithRecorder(rec)))
	r.POST("/joiner", func(c *gin.Context) {
		MarkJoined(c.Request.Context())
		c.Status(http.StatusOK)
	})
	r.POST("/normal", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, path := range []string{"/joiner", "/normal"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}

	require.Equal(t, 2, rec.callCount())

	v1, ok := findRequestJoined(rec.attrs(0))
	require.True(t, ok)
	assert.True(t, v1.AsBool(), "first (tagged) request must carry request.joined=true")

	v2, ok := findRequestJoined(rec.attrs(1))
	require.True(t, ok)
	assert.False(t, v2.AsBool(), "second (untagged) request must carry request.joined=false")
}
