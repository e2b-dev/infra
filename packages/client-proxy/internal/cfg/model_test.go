package cfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Setenv("LOKI_URL", "loki-url")
	t.Setenv("SD_ORCHESTRATOR_PROVIDER", "loki-user")

	t.Run("create parse provider", func(t *testing.T) {
		t.Setenv("SD_EDGE_PROVIDER", "sd-edge-provider")
		t.Setenv("SD_EDGE_DNS_QUERY", "sd-edge-dns-query")

		config, err := Parse()
		require.NoError(t, err)

		assert.Equal(t, "sd-edge-provider", config.EdgeServiceDiscovery.Provider)
		assert.Equal(t, []string{"sd-edge-dns-query"}, config.EdgeServiceDiscovery.DNSQuery)
	})
}
