package logger

import (
	"net"
	"net/http"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// WithRequestID extracts or generates a request ID for distributed tracing.
// Checks X-Request-ID, X-Trace-ID, and X-Correlation-ID headers in order.
func WithRequestID(r *http.Request) zap.Field {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = r.Header.Get("X-Trace-ID")
	}
	if requestID == "" {
		requestID = r.Header.Get("X-Correlation-ID")
	}
	return zap.String("request.id", requestID)
}

// WithHTTPVersion adds the HTTP protocol version.
func WithHTTPVersion(r *http.Request) zap.Field {
	return zap.String("http.version", r.Proto)
}

// WithRequestSize adds the request body size in bytes.
func WithRequestSize(r *http.Request) zap.Field {
	size := r.ContentLength
	return zap.Int64("request.size_bytes", size)
}

// WithResponseSize adds the response body size in bytes.
func WithResponseSize(responseSize int64) zap.Field {
	return zap.Int64("response.size_bytes", responseSize)
}

// WithAPIOperation constructs a standardized operation name from method and path.
// Format: "{METHOD} {PATH_PATTERN}"
// Examples: "GET /sandboxes", "POST /sandboxes/{id}/execute"
func WithAPIOperation(method, path string) zap.Field {
	operation := method + " " + path
	return zap.String("api.operation", operation)
}

// WithRemoteIP extracts the real client IP from X-Forwarded-For, X-Real-IP, or RemoteAddr.
func WithRemoteIP(r *http.Request) zap.Field {
	// 1. Get from X-Forwarded-For (take only first IP)
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		parts := strings.SplitN(ip, ",", 2)
		firstIP := strings.TrimSpace(parts[0])
		return zap.String("request.remote_ip", firstIP)
	}

	// 2. Get from X-Real-IP
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return zap.String("request.remote_ip", ip)
	}

	// 3. Get from RemoteAddr (strip port)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return zap.String("request.remote_ip", r.RemoteAddr)
	}
	return zap.String("request.remote_ip", host)
}

// WithCacheStatus indicates cache behavior (HIT, MISS, BYPASS, etc).
func WithCacheStatus(status string) zap.Field {
	return zap.String("cache.status", status)
}

// WithAPIError provides structured error information using ObjectMarshaler.
func WithAPIError(code string, message string) zap.Field {
	return zap.Object("api.error", &apiError{
		Code:    code,
		Message: message,
	})
}

// apiError implements zapcore.ObjectMarshaler for structured error logging
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("code", e.Code)
	enc.AddString("message", e.Message)
	return nil
}

// HTTPStatusToErrorCode extracts API error code from HTTP status code.
func HTTPStatusToErrorCode(statusCode int) string {
	switch statusCode {
	case 400:
		return "BAD_REQUEST"
	case 401:
		return "UNAUTHORIZED"
	case 403:
		return "FORBIDDEN"
	case 404:
		return "NOT_FOUND"
	case 409:
		return "CONFLICT"
	case 429:
		return "RATE_LIMIT"
	case 500:
		return "INTERNAL_ERROR"
	case 502:
		return "BAD_GATEWAY"
	case 503:
		return "SERVICE_UNAVAILABLE"
	case 504:
		return "TIMEOUT"
	default:
		if statusCode >= 500 {
			return "SERVER_ERROR"
		}
		if statusCode >= 400 {
			return "CLIENT_ERROR"
		}
		return "SUCCESS"
	}
}
