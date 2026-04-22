package tracing

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

func TestMiddlewareSanitizesTraceparentByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(Middleware(nooptrace.NewTracerProvider(), "test-service"))
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, c.Request.Header.Get("traceparent"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Body.String())
}
