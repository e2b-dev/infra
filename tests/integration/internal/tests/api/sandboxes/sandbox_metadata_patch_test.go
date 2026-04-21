package sandboxes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestPatchSandboxMetadata(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()

	initial := api.SandboxMetadata{
		"foo":         "bar",
		"sandboxType": "test",
	}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithMetadata(initial),
		utils.WithTimeout(60),
		utils.WithAutoPause(false),
	)

	strPtr := func(s string) *string { return &s }
	patchAndGet := func(t *testing.T, patch api.SandboxMetadataPatch) map[string]string {
		t.Helper()

		patchResp, err := client.PatchSandboxesSandboxIDMetadataWithResponse(
			ctx, sbx.SandboxID, patch, setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, patchResp.StatusCode())

		getResp, err := client.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, getResp.StatusCode())
		require.NotNil(t, getResp.JSON200)
		require.NotNil(t, getResp.JSON200.Metadata)

		return map[string]string(*getResp.JSON200.Metadata)
	}

	// Case 1: add a new key, leaving existing keys untouched.
	require.Equal(t, map[string]string{
		"foo":         "bar",
		"sandboxType": "test",
		"hello":       "world",
	}, patchAndGet(t, api.SandboxMetadataPatch{
		"hello": strPtr("world"),
	}))

	// Case 2: add one key and remove another in the same patch.
	// Empty string behaves the same as nil for removal.
	require.Equal(t, map[string]string{
		"foo":         "baz",
		"sandboxType": "test",
		"added":       "yes",
	}, patchAndGet(t, api.SandboxMetadataPatch{
		"added": strPtr("yes"),
		"hello": strPtr(""),
		"foo":   strPtr("baz"),
	}))

	// Case 3: clear every remaining key.
	require.Empty(t, patchAndGet(t, api.SandboxMetadataPatch{
		"foo":         nil,
		"sandboxType": strPtr(""),
		"added":       nil,
	}))
}
