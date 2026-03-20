package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRequestTimeout_SetsDeadline(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(RequestTimeout(500 * time.Millisecond))
	r.GET("/test", func(c *gin.Context) {
		deadline, ok := c.Request.Context().Deadline()
		assert.True(t, ok, "context should have a deadline")
		assert.WithinDuration(t, time.Now().Add(500*time.Millisecond), deadline, 100*time.Millisecond)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequestTimeout_CancelsBlockingHandler(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(RequestTimeout(100 * time.Millisecond))

	handlerDone := make(chan struct{})
	r.GET("/slow", func(c *gin.Context) {
		defer close(handlerDone)
		<-c.Request.Context().Done()
		c.Status(http.StatusServiceUnavailable)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/slow", nil)

	start := time.Now()
	r.ServeHTTP(w, req)
	elapsed := time.Since(start)

	<-handlerDone
	assert.Less(t, elapsed, 500*time.Millisecond, "handler should have been unblocked by context timeout")
}

func TestRequestTimeout_NormalRequestContextNotCanceled(t *testing.T) {
	t.Parallel()

	// Simulate an outer middleware that reads CancelCause after c.Next().
	// The context itself will be canceled by defer cancel(), but CancelCause
	// should return nil for normal (non-timed-out) requests.
	var outerCause error
	outerMiddleware := func(c *gin.Context) {
		c.Next()
		outerCause = CancelCause(c)
	}

	r := gin.New()
	r.Use(outerMiddleware)
	r.Use(RequestTimeout(5 * time.Second))
	r.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, outerCause, "CancelCause should return nil for normal requests")
}

func TestRequestTimeout_TimeoutContextVisibleToOuterMiddleware(t *testing.T) {
	t.Parallel()

	var outerCause error
	outerMiddleware := func(c *gin.Context) {
		c.Next()
		outerCause = CancelCause(c)
	}

	r := gin.New()
	r.Use(outerMiddleware)
	r.Use(RequestTimeout(50 * time.Millisecond))
	r.GET("/slow", func(c *gin.Context) {
		<-c.Request.Context().Done()
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/slow", nil)
	r.ServeHTTP(w, req)

	require.ErrorIs(t, outerCause, ErrRequestTimeout,
		"outer middleware should see ErrRequestTimeout as the cause when the timeout fires")
}
