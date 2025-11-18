package ioc

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
)

func TestAppGraph(t *testing.T) {
	tempDir := t.TempDir()

	t.Setenv("BUILD_CACHE_BUCKET_NAME", "bucket-name")
	t.Setenv("CONSUL_TOKEN", "consul-token")
	t.Setenv("DUMP_GRAPH_DOT_FILE", "graph.dot")
	t.Setenv("ENVIRONMENT", "local")
	t.Setenv("GRPC_PORT", "28485")
	t.Setenv("NODE_ID", "testing-node-id")
	t.Setenv("ORCHESTRATOR_BASE_PATH", tempDir)
	t.Setenv("ORCHESTRATOR_SERVICES", "orchestrator,template-manager")
	t.Setenv("TEMPLATE_BUCKET_NAME", "bucket-name")

	redisContainer, err := redis.Run(t.Context(), "redis:6")
	require.NoError(t, err)

	redisHost, err := redisContainer.Host(t.Context())
	require.NoError(t, err)
	redisPort, err := redisContainer.MappedPort(t.Context(), "6379")
	require.NoError(t, err)
	t.Setenv("REDIS_URL", fmt.Sprintf("%s:%d", redisHost, redisPort.Int()))

	config, err := cfg.Parse()
	require.NoError(t, err)

	app := New(config, "version", "commit-sha")
	require.NotNil(t, app)

	err = app.Start(t.Context())
	require.NoError(t, err)

	err = app.Stop(t.Context())
	require.NoError(t, err)
}
