package logger

import (
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func WithSandboxID(sandboxID string) zap.Field {
	return zap.String("sandbox.id", sandboxID)
}

func WithTemplateID(templateID string) zap.Field {
	return zap.String("template.id", templateID)
}

func WithBuildID(buildID string) zap.Field {
	return zap.String("build.id", buildID)
}

func WithExecutionID(executionID string) zap.Field {
	return zap.String("execution.id", executionID)
}

func WithUserID(userID string) zap.Field {
	return zap.String("user.id", userID)
}

func WithTeamID(teamID string) zap.Field {
	return zap.String("team.id", teamID)
}

func WithNodeID(nodeID string) zap.Field {
	return zap.String("node.id", nodeID)
}

func WithClusterID(clusterID uuid.UUID) zap.Field {
	return zap.String("cluster.id", clusterID.String())
}

func WithServiceInstanceID(instanceID string) zap.Field {
	return zap.String("service.instance.id", instanceID)
}

func WithSandboxIP(sandboxIP string) zap.Field {
	return zap.String("sandbox.ip", sandboxIP)
}

func WithEnvdVersion(envdVersion string) zap.Field {
	return zap.String("envd.version", envdVersion)
}

func WithKernelVersion(kernelVersion string) zap.Field {
	return zap.String("sandbox.kernel.version", kernelVersion)
}

func WithFirecrackerVersion(firecrackerVersion string) zap.Field {
	return zap.String("sandbox.firecracker.version", firecrackerVersion)
}

func WithClientIP(clientIP string) zap.Field {
	return zap.String("http.client_ip", clientIP)
}

func WithMaskedAPIKey(maskedAPIKey string) zap.Field {
	return zap.String("auth.api_key", maskedAPIKey)
}

func WithMaskedAccessToken(maskedAccessToken string) zap.Field {
	return zap.String("auth.access_token", maskedAccessToken)
}

// ProxyRequestFields returns the common logger fields for a proxied HTTP request.
func ProxyRequestFields(r *http.Request, sandboxID string, sandboxPort uint64) []zap.Field {
	return []zap.Field{
		zap.String("origin_host", r.Host),
		WithSandboxID(sandboxID),
		zap.Uint64("sandbox_req_port", sandboxPort),
		zap.String("sandbox_req_path", r.URL.Path),
		zap.String("sandbox_req_method", r.Method),
		zap.String("sandbox_req_user_agent", r.UserAgent()),
		zap.String("remote_addr", r.RemoteAddr),
		WithClientIP(clientIP(r)),
		zap.Int64("content_length", r.ContentLength),
	}
}

// clientIP extracts the real client IP from the request.
// It reads the first entry from X-Forwarded-For, falling back to RemoteAddr with the port stripped.
//
// This assumes a trusted upstream proxy overwrites the
// X-Forwarded-For header with the real client IP. The header value is NOT
// client-controllable in this setup because the LB always replaces it.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); ip != "" {
			return ip
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
