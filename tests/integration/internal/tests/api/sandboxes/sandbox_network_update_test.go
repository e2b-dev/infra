package sandboxes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// =============================================================================
// PUT /sandboxes/{sandboxID}/network — Dynamic network config update tests
// =============================================================================

// TestUpdateNetworkConfig_Success creates a sandbox with restrictive egress,
// then updates the network config and verifies 204.
func TestUpdateNetworkConfig_Success(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Create sandbox with deny-all egress
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Update to allow a specific IP
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: &[]string{"8.8.8.8"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())
}

// TestUpdateNetworkConfig_ClearRules creates a sandbox with restrictive egress,
// then sends an empty body to clear all rules.
func TestUpdateNetworkConfig_ClearRules(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Create sandbox with deny-all egress
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Clear all egress rules by omitting both fields
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())
}

// TestUpdateNetworkConfig_ReplaceRules verifies PUT semantics: the new config
// fully replaces the old one (not additive).
func TestUpdateNetworkConfig_ReplaceRules(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Create sandbox allowing 8.8.8.8
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &[]string{"8.8.8.8"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Replace: now allow 1.1.1.1 instead
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: &[]string{"1.1.1.1"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// TODO(stage-3): verify actual network behavior:
	// envdClient := setup.GetEnvdClient(t, ctx)
	// assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "New allow rule should work")
	// assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Old allow rule should be gone")
}

// TestUpdateNetworkConfig_NotFound returns 404 for a nonexistent sandbox.
func TestUpdateNetworkConfig_NotFound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()

	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, "nonexistent-sandbox-id",
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: &[]string{"8.8.8.8"},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode())
}

// TestUpdateNetworkConfig_Unauthorized returns 401 without an API key.
func TestUpdateNetworkConfig_Unauthorized(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()

	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, "any-sandbox-id",
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode())
}

// TestUpdateNetworkConfig_MultipleUpdates verifies that multiple sequential
// updates all succeed (each fully replacing the previous config).
func TestUpdateNetworkConfig_MultipleUpdates(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
	)

	// First update: deny all
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Second update: allow specific IP
	resp, err = client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: &[]string{"8.8.8.8"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Third update: clear all rules
	resp, err = client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())
}
