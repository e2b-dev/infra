package cfg

import (
	"encoding/base64"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	// set base required values
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres-connection-string")
	t.Setenv("LOKI_URL", "http://loki:3100")
	t.Setenv("VOLUME_TOKEN_ISSUER", "local.e2b-dev.com")
	t.Setenv("VOLUME_TOKEN_SIGNING_METHOD", "HS256")
	t.Setenv("VOLUME_TOKEN_SIGNING_KEY", fmt.Sprintf("HMAC:%s", base64.StdEncoding.EncodeToString([]byte("secret"))))
	t.Setenv("VOLUME_TOKEN_SIGNING_KEY_NAME", "my-key-name")

	for _, variable := range []string{"CLICKHOUSE_LOGS_READ_ENABLED", "CLICKHOUSE_LOGS_WRITE_ONLY"} {
		t.Run(variable+" parses strictly", func(t *testing.T) { //nolint:paralleltest // mutates process environment
			removeEnv(t, "CLICKHOUSE_LOGS_READ_ENABLED")
			removeEnv(t, variable)
			config, err := Parse()
			require.NoError(t, err)
			assert.False(t, config.ClickhouseLogsReadEnabled)
			assert.False(t, config.ClickhouseLogsWriteOnly)

			t.Setenv(variable, "true")
			config, err = Parse()
			require.NoError(t, err)
			if variable == "CLICKHOUSE_LOGS_READ_ENABLED" {
				assert.True(t, config.ClickhouseLogsReadEnabled)
				assert.False(t, config.ClickhouseLogsWriteOnly)
			} else {
				assert.False(t, config.ClickhouseLogsReadEnabled)
				assert.True(t, config.ClickhouseLogsWriteOnly)
			}

			t.Setenv(variable, "false")
			config, err = Parse()
			require.NoError(t, err)
			assert.False(t, config.ClickhouseLogsReadEnabled)
			assert.False(t, config.ClickhouseLogsWriteOnly)

			t.Setenv(variable, "invalid")
			_, err = Parse()
			assert.Error(t, err)
		})
	}

	t.Run("postgres connection string is required", func(t *testing.T) { //nolint:paralleltest // cannot call t.Setenv and t.Parallel
		removeEnv(t, "POSTGRES_CONNECTION_STRING")

		_, err := Parse()
		assert.ErrorContains(t, err, `required environment variable "POSTGRES_CONNECTION_STRING" is not set`)
	})

	t.Run("postgres connection string cannot be empty", func(t *testing.T) {
		t.Setenv("POSTGRES_CONNECTION_STRING", "")

		_, err := Parse()
		assert.ErrorContains(t, err, `environment variable "POSTGRES_CONNECTION_STRING" should not be empty`)
	})

	t.Run("base64 signing key can be parsed", func(t *testing.T) {
		content := []byte{1, 2, 3, 4, 5, 6}
		encoded := base64.StdEncoding.EncodeToString(content)
		t.Setenv("VOLUME_TOKEN_SIGNING_KEY", fmt.Sprintf("HMAC:%s", encoded))

		result, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, content, result.VolumesToken.SigningKey)
	})

	t.Run("default persistent volume type by region is parsed as a map", func(t *testing.T) {
		t.Setenv("DEFAULT_PERSISTENT_VOLUME_TYPE_BY_REGION", "us-west3:zonalfilestore-us-west3,other:other-type")

		result, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"us-west3": "zonalfilestore-us-west3",
			"other":    "other-type",
		}, result.DefaultPersistentVolumeTypeByRegion)
	})

	t.Run("secrets store backend address is optional and has no default", func(t *testing.T) { //nolint:paralleltest // cannot call t.Setenv and t.Parallel
		removeEnv(t, "SECRETS_STORE_BACKEND_GRPC_ADDRESS")

		result, err := Parse()
		require.NoError(t, err)
		assert.Empty(t, result.SecretsStoreBackendGrpcAddress)
	})

	t.Run("secrets store backend address is read when set", func(t *testing.T) {
		t.Setenv("SECRETS_STORE_BACKEND_GRPC_ADDRESS", "secrets-backend:5000")

		result, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, "secrets-backend:5000", result.SecretsStoreBackendGrpcAddress)
	})

	t.Run("invalid service discovery provider exposes failure condition", func(t *testing.T) {
		t.Setenv("SERVICE_DISCOVERY_PROVIDER", "invalid")

		_, err := Parse()
		require.Error(t, err)

		condition, ok := ParseFailureCondition(err)
		require.True(t, ok)
		assert.Equal(t, FailureConditionInvalidServiceDiscoveryProvider, condition)
	})
}

// removeEnv was mostly copied from the implementation of t.Setenv
func removeEnv(t *testing.T, key string) {
	t.Helper()

	prevValue, ok := os.LookupEnv(key)

	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("cannot unset environment variable: %v", err)
	}

	if ok {
		t.Cleanup(func() {
			os.Setenv(key, prevValue) //nolint:usetesting // we're doing fancy things here
		})
	} else {
		t.Cleanup(func() {
			os.Unsetenv(key)
		})
	}
}
