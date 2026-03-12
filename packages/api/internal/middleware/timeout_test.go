package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestTimeout_SetsDeadline(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestTimeout(500 * time.Millisecond))
	r.GET("/test", func(c *gin.Context) {
		deadline, ok := c.Request.Context().Deadline()
		assert.True(t, ok, "context should have a deadline")
		assert.WithinDuration(t, time.Now().Add(500*time.Millisecond), deadline, 100*time.Millisecond)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequestTimeout_CancelsBlockingHandler(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestTimeout(100 * time.Millisecond))

	handlerDone := make(chan struct{})
	r.GET("/slow", func(c *gin.Context) {
		defer close(handlerDone)
		<-c.Request.Context().Done()
		c.Status(http.StatusServiceUnavailable)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/slow", nil)

	start := time.Now()
	r.ServeHTTP(w, req)
	elapsed := time.Since(start)

	<-handlerDone
	assert.Less(t, elapsed, 500*time.Millisecond, "handler should have been unblocked by context timeout")
}

func TestRequestTimeout_ExcludedRouteHasNoDeadline(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestTimeout(500*time.Millisecond, "/health"))
	r.GET("/health", func(c *gin.Context) {
		_, ok := c.Request.Context().Deadline()
		assert.False(t, ok, "excluded route should not have a deadline")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequestTimeout_ExcludedRouteWithParam(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestTimeout(500*time.Millisecond, "/templates/:templateID/builds/:buildID/logs"))
	r.GET("/templates/:templateID/builds/:buildID/logs", func(c *gin.Context) {
		_, ok := c.Request.Context().Deadline()
		assert.False(t, ok, "excluded parameterized route should not have a deadline")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/templates/abc123/builds/build456/logs", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}
