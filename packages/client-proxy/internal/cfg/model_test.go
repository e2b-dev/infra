package cfg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse_Defaults(t *testing.T) {
	t.Setenv("HEALTH_PORT", "")
	t.Setenv("PROXY_PORT", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_CLUSTER_URL", "")
	t.Setenv("REDIS_TLS_CA_BASE64", "")
	t.Setenv("REDIS_POOL_SIZE", "")
	t.Setenv("API_INTERNAL_GRPC_ADDRESS", "")
	t.Setenv("API_EDGE_GRPC_ADDRESS", "")
	t.Setenv("API_EDGE_GRPC_OAUTH_CLIENT_ID", "")
	t.Setenv("API_EDGE_GRPC_OAUTH_CLIENT_SECRET", "")
	t.Setenv("API_EDGE_GRPC_OAUTH_TOKEN_URL", "")

	cfg, err := Parse()
	require.NoError(t, err)
	require.EqualValues(t, 3003, cfg.HealthPort)
	require.EqualValues(t, 3002, cfg.ProxyPort)
	require.Equal(t, 40, cfg.RedisPoolSize)
	require.Empty(t, cfg.RedisURL)
	require.Empty(t, cfg.RedisClusterURL)
	require.Empty(t, cfg.RedisTLSCABase64)
	require.Empty(t, cfg.APIInternalGRPCAddress)
	require.Empty(t, cfg.APIEdgeGRPCAddress)
	require.Empty(t, cfg.APIEdgeGRPCOAuthClientID)
	require.Empty(t, cfg.APIEdgeGRPCOAuthClientSecret)
	require.Empty(t, cfg.APIEdgeGRPCOAuthTokenURL)
}

func TestParse_OverridesFromEnv(t *testing.T) {
	t.Setenv("HEALTH_PORT", "9001")
	t.Setenv("PROXY_PORT", "9002")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("REDIS_CLUSTER_URL", "redis://cluster:6379")
	t.Setenv("REDIS_TLS_CA_BASE64", "Y2EtZGF0YQ==")
	t.Setenv("REDIS_POOL_SIZE", "12")
	t.Setenv("API_INTERNAL_GRPC_ADDRESS", "internal:5005")
	t.Setenv("API_EDGE_GRPC_ADDRESS", "edge:5006")
	t.Setenv("API_EDGE_GRPC_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("API_EDGE_GRPC_OAUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("API_EDGE_GRPC_OAUTH_TOKEN_URL", "https://tokens.example.com")

	cfg, err := Parse()
	require.NoError(t, err)
	require.EqualValues(t, 9001, cfg.HealthPort)
	require.EqualValues(t, 9002, cfg.ProxyPort)
	require.Equal(t, "redis://localhost:6379", cfg.RedisURL)
	require.Equal(t, "redis://cluster:6379", cfg.RedisClusterURL)
	require.Equal(t, "Y2EtZGF0YQ==", cfg.RedisTLSCABase64)
	require.Equal(t, 12, cfg.RedisPoolSize)
	require.Equal(t, "internal:5005", cfg.APIInternalGRPCAddress)
	require.Equal(t, "edge:5006", cfg.APIEdgeGRPCAddress)
	require.Equal(t, "client-id", cfg.APIEdgeGRPCOAuthClientID)
	require.Equal(t, "client-secret", cfg.APIEdgeGRPCOAuthClientSecret)
	require.Equal(t, "https://tokens.example.com", cfg.APIEdgeGRPCOAuthTokenURL)
}

func TestParse_InvalidIntegerReturnsError(t *testing.T) {
	t.Setenv("HEALTH_PORT", "not-a-number")

	_, err := Parse()
	require.Error(t, err)
}
