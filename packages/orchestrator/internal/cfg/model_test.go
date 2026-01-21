package cfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Run("embedded structs get defaults", func(t *testing.T) { //nolint:paralleltest // siblings set env, which may cause issues
		config, err := Parse()
		require.NoError(t, err)

		assert.Equal(t, "/fc-vm", config.SandboxDir)
	})

	t.Run("embedded structs get overrides", func(t *testing.T) {
		t.Setenv("SANDBOX_DIR", "/fc-vm2")

		config, err := Parse()
		require.NoError(t, err)

		assert.Equal(t, "/fc-vm2", config.SandboxDir)
	})

	t.Run("network config local flag defaults to false", func(t *testing.T) { //nolint:paralleltest // siblings set env, which may cause issues
		config, err := Parse()
		require.NoError(t, err)

		assert.False(t, config.NetworkConfig.UseLocalNamespaceStorage)
	})

	t.Run("network config is parsed correctly", func(t *testing.T) {
		t.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")

		config, err := Parse()
		require.NoError(t, err)

		assert.True(t, config.NetworkConfig.UseLocalNamespaceStorage)
	})

	t.Run("multiple services parses correctly", func(t *testing.T) {
		t.Setenv("ORCHESTRATOR_SERVICES", "service1,service2")

		config, err := Parse()
		require.NoError(t, err)

		assert.Equal(t, []string{"service1", "service2"}, config.Services)
	})

	t.Run("env defaults get defaults before expansion", func(t *testing.T) { //nolint:paralleltest // siblings set env, which may cause issues
		config, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, "/orchestrator/build", config.DefaultCacheDir)
	})

	t.Run("env defaults get expanded", func(t *testing.T) {
		t.Setenv("ORCHESTRATOR_BASE_PATH", "/a/b/c")
		config, err := Parse()
		require.NoError(t, err)
		assert.Equal(t, "/a/b/c/build", config.DefaultCacheDir)
		assert.Equal(t, "/a/b/c/sandbox", config.StorageConfig.SandboxCacheDir)
	})
}
