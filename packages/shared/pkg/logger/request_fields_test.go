package logger

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithRequestID_ExtractionOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		headers  map[string]string
		expected string
	}{
		{
			name: "X-Request-ID takes priority",
			headers: map[string]string{
				"X-Request-ID":     "req_001",
				"X-Trace-ID":       "trace_001",
				"X-Correlation-ID": "corr_001",
			},
			expected: "req_001",
		},
		{
			name: "X-Trace-ID fallback when X-Request-ID absent",
			headers: map[string]string{
				"X-Trace-ID":       "trace_002",
				"X-Correlation-ID": "corr_002",
			},
			expected: "trace_002",
		},
		{
			name: "X-Correlation-ID last resort",
			headers: map[string]string{
				"X-Correlation-ID": "corr_003",
			},
			expected: "corr_003",
		},
		{
			name:     "Empty when no headers present",
			headers:  map[string]string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for key, val := range tt.headers {
				req.Header.Set(key, val)
			}

			field := WithRequestID(req)
			assert.Equal(t, "request.id", field.Key)
			assert.Equal(t, tt.expected, field.String)
		})
	}
}

func TestWithHTTPVersion(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Proto = "HTTP/2.0"

	field := WithHTTPVersion(req)
	assert.Equal(t, "http.version", field.Key)
	assert.Equal(t, "HTTP/2.0", field.String)
}

func TestWithRequestSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		size     int64
		expected int64
	}{
		{
			name:     "positive content length",
			size:     1024,
			expected: 1024,
		},
		{
			name:     "zero content length",
			size:     0,
			expected: 0,
		},
		{
			name:     "large content length",
			size:     1024 * 1024 * 100, // 100MB
			expected: 1024 * 1024 * 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/test", nil)
			req.ContentLength = tt.size

			field := WithRequestSize(req)
			assert.Equal(t, "request.size_bytes", field.Key)
			assert.Equal(t, tt.expected, field.Integer)
		})
	}
}

func TestWithResponseSize(t *testing.T) {
	t.Parallel()

	field := WithResponseSize(2048)
	assert.Equal(t, "response.size_bytes", field.Key)
	assert.Equal(t, int64(2048), field.Integer)
}

func TestWithAPIOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		path     string
		expected string
	}{
		{
			name:     "GET request",
			method:   "GET",
			path:     "/api/v1/sandboxes",
			expected: "GET /api/v1/sandboxes",
		},
		{
			name:     "POST request with ID",
			method:   "POST",
			path:     "/api/v1/sandboxes/{id}/execute",
			expected: "POST /api/v1/sandboxes/{id}/execute",
		},
		{
			name:     "DELETE request",
			method:   "DELETE",
			path:     "/api/v1/sandboxes/abc123",
			expected: "DELETE /api/v1/sandboxes/abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := WithAPIOperation(tt.method, tt.path)
			assert.Equal(t, "api.operation", field.Key)
			assert.Equal(t, tt.expected, field.String)
		})
	}
}

func TestWithRemoteIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(*http.Request)
		expected string
	}{
		{
			name: "X-Forwarded-For header (only first IP)",
			setup: func(req *http.Request) {
				req.Header.Set("X-Forwarded-For", "203.0.113.1, 203.0.113.2")
			},
			expected: "203.0.113.1",
		},
		{
			name: "X-Real-IP header fallback",
			setup: func(req *http.Request) {
				req.Header.Set("X-Real-IP", "192.0.2.1")
			},
			expected: "192.0.2.1",
		},
		{
			name: "RemoteAddr fallback (strip port)",
			setup: func(req *http.Request) {
				req.RemoteAddr = "10.0.0.1:12345"
			},
			expected: "10.0.0.1",
		},
		{
			name: "X-Forwarded-For takes priority",
			setup: func(req *http.Request) {
				req.Header.Set("X-Forwarded-For", "203.0.113.1")
				req.Header.Set("X-Real-IP", "192.0.2.1")
				req.RemoteAddr = "10.0.0.1:12345"
			},
			expected: "203.0.113.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			tt.setup(req)

			field := WithRemoteIP(req)
			assert.Equal(t, "request.remote_ip", field.Key)
			assert.Equal(t, tt.expected, field.String)
		})
	}
}

func TestWithCacheStatus(t *testing.T) {
	t.Parallel()

	field := WithCacheStatus("HIT")
	assert.Equal(t, "cache.status", field.Key)
	assert.Equal(t, "HIT", field.String)
}

func TestWithAPIError(t *testing.T) {
	t.Parallel()

	field := WithAPIError("BAD_REQUEST", "invalid parameter")
	assert.NotNil(t, field)
	assert.Equal(t, "api.error", field.Key)
}

func TestHTTPStatusToErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status   int
		expected string
	}{
		{http.StatusBadRequest, "BAD_REQUEST"},
		{http.StatusUnauthorized, "UNAUTHORIZED"},
		{http.StatusForbidden, "FORBIDDEN"},
		{http.StatusNotFound, "NOT_FOUND"},
		{http.StatusConflict, "CONFLICT"},
		{http.StatusTooManyRequests, "RATE_LIMIT"},
		{http.StatusInternalServerError, "INTERNAL_ERROR"},
		{http.StatusBadGateway, "BAD_GATEWAY"},
		{http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE"},
		{http.StatusGatewayTimeout, "TIMEOUT"},
		{http.StatusOK, "SUCCESS"},
		{http.StatusCreated, "SUCCESS"},
		{http.StatusTeapot, "CLIENT_ERROR"},
		{http.StatusNotImplemented, "SERVER_ERROR"},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			code := HTTPStatusToErrorCode(tt.status)
			assert.Equal(t, tt.expected, code)
		})
	}
}

func TestWithRequestID_FieldKind(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "test-id-123")

	field := WithRequestID(req)
	assert.NotNil(t, field)
	assert.Equal(t, "request.id", field.Key)
	assert.Equal(t, "test-id-123", field.String)
}

func TestWithRequestSize_NoContentLength(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/test", nil)
	req.ContentLength = -1

	field := WithRequestSize(req)
	assert.Equal(t, "request.size_bytes", field.Key)
	assert.Equal(t, int64(-1), field.Integer)
}
