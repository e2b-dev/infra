package sandbox_catalog

import (
	"testing"

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
