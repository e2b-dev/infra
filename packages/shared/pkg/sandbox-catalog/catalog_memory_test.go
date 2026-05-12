package sandbox_catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemorySandboxCatalogAcquireTrafficKeepalive(t *testing.T) {
	t.Parallel()

	c := NewMemorySandboxesCatalog()
	t.Cleanup(func() { require.NoError(t, c.Close(t.Context())) })

	acquired, err := c.AcquireTrafficKeepalive(t.Context(), "sbx")
	require.NoError(t, err)
	require.True(t, acquired)

	acquired, err = c.AcquireTrafficKeepalive(t.Context(), "sbx")
	require.NoError(t, err)
	require.False(t, acquired)
}

func TestMemorySandboxCatalogPrunesExpiredTrafficKeepalives(t *testing.T) {
	t.Parallel()

	catalog := NewMemorySandboxesCatalog()
	t.Cleanup(func() { require.NoError(t, catalog.Close(t.Context())) })

	c := catalog.(*MemorySandboxCatalog)
	c.trafficKeepalives["expired"] = time.Now().Add(-time.Second)

	acquired, err := c.AcquireTrafficKeepalive(t.Context(), "sbx")
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotContains(t, c.trafficKeepalives, "expired")
}
