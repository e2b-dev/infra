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

// TestUpdateNetworkConfig_Success creates a sandbox with deny-all egress,
// then dynamically allows a specific IP and verifies connectivity changes.
func TestUpdateNetworkConfig_Success(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	// Create sandbox with deny-all egress
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Verify traffic is blocked before update
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Should be blocked before update")

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

	// Verify the allowed IP is now reachable
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Allowed IP should be reachable after update")
	// Verify other IPs are still blocked
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "Non-allowed IP should still be blocked")
}

// TestUpdateNetworkConfig_ClearRules creates a sandbox with deny-all egress,
// then clears all rules and verifies traffic flows freely.
func TestUpdateNetworkConfig_ClearRules(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	// Create sandbox with deny-all egress
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Verify traffic is blocked before update
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Should be blocked before clearing rules")

	// Clear all egress rules by omitting both fields
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Verify traffic flows freely after clearing rules
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Traffic should flow after clearing rules")
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "Traffic should flow after clearing rules")
}

// TestUpdateNetworkConfig_ReplaceRules verifies PUT semantics: the new config
// fully replaces the old one (not additive).
func TestUpdateNetworkConfig_ReplaceRules(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	// Create sandbox allowing 8.8.8.8
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &[]string{"8.8.8.8"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Verify initial config: 8.8.8.8 allowed, 1.1.1.1 blocked
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "8.8.8.8 should be allowed initially")
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "1.1.1.1 should be blocked initially")

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

	// Verify replacement: 1.1.1.1 now allowed, 8.8.8.8 now blocked
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "New allow rule should work")
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Old allow rule should be gone")
}

// TestUpdateNetworkConfig_NotFound returns 404 for a nonexistent sandbox.
func TestUpdateNetworkConfig_NotFound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()

	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, "ixxxxxxxxxxxxxxxxxx0",
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

// TestUpdateNetworkConfig_PauseResume verifies that dynamically updated
// network rules survive a pause/resume cycle.
func TestUpdateNetworkConfig_PauseResume(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	// Create sandbox with no restrictions and auto-pause disabled
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(90),
		utils.WithAutoPause(false),
	)

	// Dynamically add deny-all + allow 8.8.8.8
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: &[]string{"8.8.8.8"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Verify rules work before pause
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "8.8.8.8 should be allowed before pause")
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "1.1.1.1 should be blocked before pause")

	// Pause
	pauseResp, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	// Resume
	resumeResp, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
		api.PostSandboxesSandboxIDResumeJSONRequestBody{},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resumeResp.StatusCode())

	// Verify rules survived pause/resume
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "8.8.8.8 should still be allowed after resume")
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "1.1.1.1 should still be blocked after resume")
}

// TestUpdateNetworkConfig_AllowDomain dynamically allows a domain through
// a deny-all policy and verifies connectivity by hostname (TLS SNI).
func TestUpdateNetworkConfig_AllowDomain(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	// Create sandbox with deny-all egress
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Verify all traffic blocked
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", "Should be blocked before domain allow")

	// Dynamically allow google.com domain
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: &[]string{"google.com"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Verify allowed domain is reachable, other domains still blocked
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", "Allowed domain should be reachable")
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", "Non-allowed domain should still be blocked")
}

// TestUpdateNetworkConfig_RemoveDomain dynamically adds a domain allow rule,
// then removes it by replacing with an empty config, and verifies the domain
// becomes unreachable again.
func TestUpdateNetworkConfig_RemoveDomain(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	// Create sandbox with deny-all + allow google.com
	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &[]string{"google.com"},
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	// Verify google.com reachable
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", "google.com should be reachable initially")

	// Replace with deny-all only (no domain allow)
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Verify google.com is now blocked
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", "google.com should be blocked after removing domain allow")
}

// TestUpdateNetworkConfig_MultipleUpdates verifies that multiple sequential
// updates each change actual network behavior (not just API response).
func TestUpdateNetworkConfig_MultipleUpdates(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(90),
	)

	// Initially no restrictions — traffic should flow
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Should have internet before any updates")

	// First update: deny all
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyOut: &[]string{sandbox_network.AllInternetTrafficCIDR},
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Verify all traffic is blocked
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Should be blocked after deny-all")

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

	// Verify only allowed IP works
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "8.8.8.8 should be allowed")
	assertBlockedHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "1.1.1.1 should still be blocked")

	// Third update: clear all rules
	resp, err = client.PutSandboxesSandboxIDNetworkWithResponse(ctx, sbx.SandboxID,
		api.PutSandboxesSandboxIDNetworkJSONRequestBody{},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Verify all traffic flows again
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", "Should have internet after clearing rules")
	assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", "Should have internet after clearing rules")
}
