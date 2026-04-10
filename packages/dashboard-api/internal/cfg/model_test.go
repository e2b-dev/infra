package cfg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRequiresAdminToken(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("REDIS_CLUSTER_URL", "")
	t.Setenv("AUTH_DB_CONNECTION_STRING", "")
	t.Setenv("AUTH_DB_READ_REPLICA_CONNECTION_STRING", "")
	t.Setenv("ADMIN_TOKEN", "")

	_, err := Parse()
	require.Error(t, err)
	require.ErrorContains(t, err, "ADMIN_TOKEN")
}

func TestParseAcceptsAdminToken(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("REDIS_CLUSTER_URL", "")
	t.Setenv("AUTH_DB_CONNECTION_STRING", "")
	t.Setenv("AUTH_DB_READ_REPLICA_CONNECTION_STRING", "")
	t.Setenv("ADMIN_TOKEN", "secret")

	config, err := Parse()
	require.NoError(t, err)
	require.Equal(t, "secret", config.AdminToken)
	require.Equal(t, "postgres://test", config.AuthDBConnectionString)
}
