package cfg

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	// set base required values
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres-connection-string")
	t.Setenv("LOKI_URL", "http://loki:3100")

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

	t.Run("supabase secrets are comma separated", func(t *testing.T) {
		t.Setenv("SUPABASE_JWT_SECRETS", "aaa,bbb")
		result, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, []string{"aaa", "bbb"}, result.SupabaseJWTSecrets)
	})

	t.Run("default persistent volume type must be in types", func(t *testing.T) {
		t.Setenv("PERSISTENT_VOLUME_TYPES", "valid:1,other:2")

		_, err := Parse()
		require.NoError(t, err, "no default is acceptable")

		t.Setenv("DEFAULT_PERSISTENT_VOLUME_TYPE", "invalid")
		_, err = Parse()
		require.Error(t, err, "invalid default is not acceptable")

		t.Setenv("DEFAULT_PERSISTENT_VOLUME_TYPE", "valid")
		_, err = Parse()
		require.NoError(t, err, "valid default is acceptable")
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
