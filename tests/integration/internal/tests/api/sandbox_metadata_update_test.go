package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxMetadataUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create a sandbox with initial metadata
	initialMetadata := api.SandboxMetadata{
		"sandboxType": "test",
		"version":     "1.0.0",
		"environment": "supr-cupr",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(initialMetadata),
		utils.WithTimeout(60),
	)

	// Verify the sandbox was created with initial metadata
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)

	// Verify initial metadata exists
	assert.NotNil(t, getSandboxResponse.JSON200.Metadata)
	assert.Equal(t, "test", (*getSandboxResponse.JSON200.Metadata)["sandboxType"])
	assert.Equal(t, "1.0.0", (*getSandboxResponse.JSON200.Metadata)["version"])
	assert.Equal(t, "supr-cupr", (*getSandboxResponse.JSON200.Metadata)["environment"])

	// Update metadata using PATCH
	updateMetadata := api.SandboxMetadata{
		"environment": "e2b-is-place-to-be", // Update existing key
		"version":     "1.1.0",              // Update existing key
		"branch":      "feature-test",       // Add new key
		// Note: "sandboxType" key is not included, should remain unchanged
	}

	updateResponse, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: &updateMetadata,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	if t.Failed() {
		t.Logf("Update Response: %s", string(updateResponse.Body))
	}

	// Verify metadata was updated correctly
	getUpdatedSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getUpdatedSandboxResponse.StatusCode())
	require.NotNil(t, getUpdatedSandboxResponse.JSON200)
	require.NotNil(t, getUpdatedSandboxResponse.JSON200.Metadata)

	updatedMeta := *getUpdatedSandboxResponse.JSON200.Metadata

	// Verify updated values
	assert.Equal(t, "e2b-is-place-to-be", updatedMeta["environment"])
	assert.Equal(t, "1.1.0", updatedMeta["version"])
	assert.Equal(t, "feature-test", updatedMeta["branch"])

	// Verify unchanged value
	assert.Equal(t, "test", updatedMeta["sandboxType"]) // Should remain unchanged

	// Verify we have all expected keys
	assert.Len(t, updatedMeta, 4)
}

func TestSandboxMetadataUpdateEmpty(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create sandbox with existing metadata
	initialMetadata := api.SandboxMetadata{
		"test": "value",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(initialMetadata),
		utils.WithTimeout(30),
	)

	// Update with empty metadata
	emptyMetadata := api.SandboxMetadata{}

	updateResponse, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: &emptyMetadata,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	// Verify original metadata is still there (empty metadata shouldn't clear existing)
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)
	require.NotNil(t, getSandboxResponse.JSON200.Metadata)

	metadata := *getSandboxResponse.JSON200.Metadata
	assert.Equal(t, "value", metadata["test"])
}

func TestSandboxMetadataUpdateNil(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create sandbox with existing metadata
	initialMetadata := api.SandboxMetadata{
		"existing": "data",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(initialMetadata),
		utils.WithTimeout(30),
	)

	// Update with nil metadata
	updateResponse, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: nil,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	// Verify original metadata is unchanged
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)
	require.NotNil(t, getSandboxResponse.JSON200.Metadata)

	metadata := *getSandboxResponse.JSON200.Metadata
	assert.Equal(t, "data", metadata["existing"])
}

func TestSandboxMetadataUpdateCheckEndTime(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create sandbox with existing metadata
	initialMetadata := api.SandboxMetadata{
		"existing": "data",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(initialMetadata),
		utils.WithTimeout(30),
	)
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)

	initialEndTime := getSandboxResponse.JSON200.EndAt

	// Update with nil metadata
	updateResponse, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: nil,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	getSandboxResponse, err = c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)

	// Verify original end time is unchanged
	assert.Equal(t, initialEndTime, getSandboxResponse.JSON200.EndAt)
}

func TestSandboxMetadataUpdateNonExistentSandbox(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Try to update metadata on non-existent sandbox
	updateMetadata := api.SandboxMetadata{
		"test": "value",
	}

	updateResponse, err := c.PatchSandboxesSandboxIDWithResponse(ctx, "nonexistent-sandbox-id", api.SandboxUpdateRequest{
		Metadata: &updateMetadata,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, updateResponse.StatusCode())
}

func TestSandboxMetadataUpdateWithSpecialCharacters(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	sandbox := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(30))

	// Update with metadata containing special characters
	specialMetadata := api.SandboxMetadata{
		"user.email":   "test@example.com",
		"config/path":  "/opt/app/config",
		"build-number": "2024.01.15",
		"feature_flag": "true",
		"emoji":        "🚀",
		"unicode":      "αβγδε",
		"json_like":    `{"key": "value"}`,
		"spaces":       "value with spaces",
	}

	updateResponse, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: &specialMetadata,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	// Verify special characters are preserved
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)
	require.NotNil(t, getSandboxResponse.JSON200.Metadata)

	metadata := *getSandboxResponse.JSON200.Metadata
	assert.Equal(t, "test@example.com", metadata["user.email"])
	assert.Equal(t, "/opt/app/config", metadata["config/path"])
	assert.Equal(t, "2024.01.15", metadata["build-number"])
	assert.Equal(t, "true", metadata["feature_flag"])
	assert.Equal(t, "🚀", metadata["emoji"])
	assert.Equal(t, "αβγδε", metadata["unicode"])
	assert.Equal(t, `{"key": "value"}`, metadata["json_like"])
	assert.Equal(t, "value with spaces", metadata["spaces"])
}

func TestSandboxMetadataUpdateInvalidAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	sandbox := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(30))

	updateMetadata := api.SandboxMetadata{
		"test": "value",
	}

	// Try to update without API key
	updateResponse, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: &updateMetadata,
	})

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, updateResponse.StatusCode())

	// Try to update with invalid API key
	updateResponse, err = c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: &updateMetadata,
	}, setup.WithAPIKey("invalid-api-key"))

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, updateResponse.StatusCode())
}

func TestSandboxMetadataUpdateMultipleUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create sandbox with initial metadata
	initialMetadata := api.SandboxMetadata{
		"version": "1.0.0",
		"env":     "test",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(initialMetadata),
		utils.WithTimeout(60),
	)

	// First update
	firstUpdate := api.SandboxMetadata{
		"version": "1.1.0",
		"branch":  "feature-a",
	}

	updateResponse1, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: &firstUpdate,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse1.StatusCode())

	// Second update
	secondUpdate := api.SandboxMetadata{
		"version":  "1.2.0",
		"build_id": "abc123",
		"deployed": "true",
	}

	updateResponse2, err := c.PatchSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, api.SandboxUpdateRequest{
		Metadata: &secondUpdate,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse2.StatusCode())

	// Verify final state
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx, sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)
	require.NotNil(t, getSandboxResponse.JSON200.Metadata)

	metadata := *getSandboxResponse.JSON200.Metadata

	// Verify cumulative updates
	assert.Equal(t, "1.2.0", metadata["version"])    // Updated in second update
	assert.Equal(t, "test", metadata["env"])         // From initial, unchanged
	assert.Equal(t, "feature-a", metadata["branch"]) // From first update, unchanged
	assert.Equal(t, "abc123", metadata["build_id"])  // From second update
	assert.Equal(t, "true", metadata["deployed"])    // From second update

	// Should have: version, env, branch, build_id, deployed, plus sandboxType from utils
	assert.Len(t, metadata, 6)
}
