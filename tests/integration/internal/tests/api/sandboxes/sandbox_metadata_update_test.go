package sandboxes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestUpdateSandboxMetadata(t *testing.T) {
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

	// Step 1: full replace.
	replacement := api.SandboxMetadata{
		"hello": "world",
		"n":     "1",
	}

	putResp, err := client.PutSandboxesSandboxIDMetadataWithResponse(
		ctx, sbx.SandboxID, replacement,
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, putResp.StatusCode())

	getResp, err := client.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getResp.StatusCode())
	require.NotNil(t, getResp.JSON200)
	require.NotNil(t, getResp.JSON200.Metadata)
	require.Equal(t, map[string]string(replacement), map[string]string(*getResp.JSON200.Metadata))

	// Step 2: empty body erases all tags.
	putResp, err = client.PutSandboxesSandboxIDMetadataWithResponse(
		ctx, sbx.SandboxID, api.SandboxMetadata{},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, putResp.StatusCode())

	getResp, err = client.GetSandboxesSandboxIDWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getResp.StatusCode())
	require.NotNil(t, getResp.JSON200)
	if getResp.JSON200.Metadata != nil {
		require.Empty(t, *getResp.JSON200.Metadata)
	}
}
