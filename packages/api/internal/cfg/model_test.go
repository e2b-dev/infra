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

	t.Run("hmac secrets are parsed from auth provider config", func(t *testing.T) {
		t.Setenv("AUTH_PROVIDER_CONFIG", `{"bearer":[{"hmac":{"secrets":["aaa","bbb"]}}]}`)
		result, err := Parse()
		require.NoError(t, err)
		require.Len(t, result.AuthProvider.Bearer, 1)
		require.NotNil(t, result.AuthProvider.Bearer[0].HMAC)
		assert.Equal(t, []string{"aaa", "bbb"}, result.AuthProvider.Bearer[0].HMAC.Secrets)
	})

	t.Run("base64 signing key can be parsed", func(t *testing.T) {
		content := []byte{1, 2, 3, 4, 5, 6}
		encoded := base64.StdEncoding.EncodeToString(content)
		t.Setenv("VOLUME_TOKEN_SIGNING_KEY", fmt.Sprintf("HMAC:%s", encoded))

		result, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, content, result.VolumesToken.SigningKey)
	})

	t.Run("test sandbox backend empty string", func(t *testing.T) {
		t.Setenv("SANDBOX_STORAGE_BACKEND", "")
		result, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, SandboxStorageBackendMemory, result.SandboxStorageBackend)
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
