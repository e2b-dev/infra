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
	require.NoError(t, c.StoreSandbox(t.Context(), "sbx", &SandboxInfo{ExecutionID: "exec"}, time.Minute))

	acquired, err := c.AcquireTrafficKeepalive(t.Context(), "sbx")
	require.NoError(t, err)
	require.True(t, acquired)

	acquired, err = c.AcquireTrafficKeepalive(t.Context(), "sbx")
	require.NoError(t, err)
	require.False(t, acquired)
}

func TestMemorySandboxCatalogReleaseTrafficKeepalive(t *testing.T) {
	t.Parallel()

	c := NewMemorySandboxesCatalog()
	t.Cleanup(func() { require.NoError(t, c.Close(t.Context())) })
	require.NoError(t, c.StoreSandbox(t.Context(), "sbx", &SandboxInfo{ExecutionID: "exec"}, time.Minute))

	acquired, err := c.AcquireTrafficKeepalive(t.Context(), "sbx")
	require.NoError(t, err)
	require.True(t, acquired)

	require.NoError(t, c.ReleaseTrafficKeepalive(t.Context(), "sbx"))

	acquired, err = c.AcquireTrafficKeepalive(t.Context(), "sbx")
	require.NoError(t, err)
	require.True(t, acquired)
}

func TestMemorySandboxCatalogAcquireTrafficKeepaliveRequiresCatalogEntry(t *testing.T) {
	t.Parallel()

	c := NewMemorySandboxesCatalog()
	t.Cleanup(func() { require.NoError(t, c.Close(t.Context())) })

	acquired, err := c.AcquireTrafficKeepalive(t.Context(), "missing")
	require.ErrorIs(t, err, ErrSandboxNotFound)
	require.False(t, acquired)
}
