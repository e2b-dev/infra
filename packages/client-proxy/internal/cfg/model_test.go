package cfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Setenv("LOKI_URL", "loki-url")
	t.Setenv("SD_ORCHESTRATOR_PROVIDER", "STATIC")

	t.Run("create parse provider", func(t *testing.T) {
		t.Setenv("SD_ORCHESTRATOR_PROVIDER", "STATIC")
		t.Setenv("SD_ORCHESTRATOR_DNS_QUERY", "10.11.11.1")

		config, err := Parse()
		require.NoError(t, err)

		assert.Equal(t, "STATIC", config.OrchestratorServiceDiscovery.Provider)
		assert.Equal(t, []string{"10.11.11.1"}, config.OrchestratorServiceDiscovery.DNSQuery)
	})
}
