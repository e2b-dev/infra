package logger

import (
	"fmt"
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

func WithClientIP(clientIP string) zap.Field {
	return zap.String("http.client_ip", clientIP)
}

func WithAPIKey(prefix, apiKey string) zap.Field {
	return zap.String("auth.api_key", tokenHint(prefix, apiKey))
}

func WithAccessToken(prefix, accessToken string) zap.Field {
	return zap.String("auth.access_token", tokenHint(prefix, accessToken))
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

// tokenHint returns a masked version of the token value for debugging.
// It strips the prefix and shows the first 2 and last 2 characters (e.g. "<prefix>ab...9f").
func tokenHint(prefix string, token string) string {
	value := strings.TrimPrefix(token, prefix)
	if len(value) < 5 {
		return fmt.Sprintf("%s...", prefix)
	}

	return fmt.Sprintf("%s%s...%s", prefix, value[:2], value[len(value)-2:])
}

// clientIP extracts the real client IP from the request.
// It reads the first entry from X-Forwarded-For, falling back to RemoteAddr with the port stripped.
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
