package cfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Run("network config local flag defaults to false", func(t *testing.T) {
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
}
