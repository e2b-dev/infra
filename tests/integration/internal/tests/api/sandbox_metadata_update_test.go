package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxMetadataUpdate(t *testing.T) {
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
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)

	// Verify initial metadata exists
	assert.NotNil(t, getSandboxResponse.JSON200.Metadata)
	assert.Equal(t, "test", (*getSandboxResponse.JSON200.Metadata)["sandboxType"])
	assert.Equal(t, "1.0.0", (*getSandboxResponse.JSON200.Metadata)["version"])
	assert.Equal(t, "supr-cupr", (*getSandboxResponse.JSON200.Metadata)["environment"])

	// Update metadata using PUT (replaces all metadata)
	updateMetadata := api.SandboxMetadata{
		"environment": "e2b-is-place-to-be", // Update existing key
		"version":     "1.1.0",              // Update existing key
		"branch":      "feature-test",       // Add new key
		// Note: "sandboxType" key is not included, will be removed with PUT
	}

	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, updateMetadata, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	if t.Failed() {
		t.Logf("Update Response: %s", string(updateResponse.Body))
	}

	// Verify metadata was updated correctly
	getUpdatedSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getUpdatedSandboxResponse.StatusCode())
	require.NotNil(t, getUpdatedSandboxResponse.JSON200)
	require.NotNil(t, getUpdatedSandboxResponse.JSON200.Metadata)

	updatedMeta := *getUpdatedSandboxResponse.JSON200.Metadata

	// Verify updated values
	assert.Equal(t, "e2b-is-place-to-be", updatedMeta["environment"])
	assert.Equal(t, "1.1.0", updatedMeta["version"])
	assert.Equal(t, "feature-test", updatedMeta["branch"])

	// Verify sandboxType was removed
	_, hasSandboxType := updatedMeta["sandboxType"]
	assert.False(t, hasSandboxType, "sandboxType should be removed as it was not in PUT request")

	// Verify we have only the keys from PUT request
	assert.Len(t, updatedMeta, 3)
}

func TestSandboxMetadataUpdateEmpty(t *testing.T) {
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
	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, emptyMetadata, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	// Verify metadata is now empty (PUT with empty replaces all)
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)

	// Metadata should be empty or nil after PUT with empty map
	if getSandboxResponse.JSON200.Metadata != nil {
		metadata := *getSandboxResponse.JSON200.Metadata
		assert.Empty(t, metadata, "Metadata should be empty after PUT with empty map")
	}
}

func TestSandboxMetadataUpdateCheckEndTime(t *testing.T) {
	c := setup.GetAPIClient()

	// Create sandbox with existing metadata
	initialMetadata := api.SandboxMetadata{
		"existing": "data",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(initialMetadata),
		utils.WithTimeout(30),
	)
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)

	initialEndTime := getSandboxResponse.JSON200.EndAt

	// Update metadata with PUT (keeping same metadata)
	updateMetadata := api.SandboxMetadata{
		"existing": "data",
	}
	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, updateMetadata, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	getSandboxResponse, err = c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)

	// Verify original end time is unchanged
	assert.Equal(t, initialEndTime, getSandboxResponse.JSON200.EndAt)
}

func TestSandboxMetadataUpdateNonExistentSandbox(t *testing.T) {
	c := setup.GetAPIClient()

	// Try to update metadata on non-existent sandbox
	updateMetadata := api.SandboxMetadata{
		"test": "value",
	}

	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), "nonexistent-sandbox-id", updateMetadata, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, updateResponse.StatusCode())
}

func TestSandboxMetadataUpdateWithSpecialCharacters(t *testing.T) {
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

	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, specialMetadata, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse.StatusCode())

	// Verify special characters are preserved
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
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
	c := setup.GetAPIClient()

	sandbox := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(30))

	updateMetadata := api.SandboxMetadata{
		"test": "value",
	}

	// Try to update without API key
	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, updateMetadata)

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, updateResponse.StatusCode())

	// Try to update with invalid API key
	updateResponse, err = c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, updateMetadata, setup.WithAPIKey("invalid-api-key"))

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, updateResponse.StatusCode())
}

func TestSandboxMetadataUpdateMultipleUpdates(t *testing.T) {
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

	// First update - PUT replaces all metadata
	firstUpdate := api.SandboxMetadata{
		"version": "1.1.0",
		"env":     "test", // Need to include existing keys to preserve them
		"branch":  "feature-a",
	}

	updateResponse1, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, firstUpdate, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse1.StatusCode())

	// Second update - PUT replaces all metadata
	secondUpdate := api.SandboxMetadata{
		"version":  "1.2.0",
		"env":      "test",      // Need to preserve from initial
		"branch":   "feature-a", // Need to preserve from first update
		"build_id": "abc123",
		"deployed": "true",
	}

	updateResponse2, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, secondUpdate, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResponse2.StatusCode())

	// Verify final state
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)
	require.NotNil(t, getSandboxResponse.JSON200.Metadata)

	metadata := *getSandboxResponse.JSON200.Metadata

	// Verify final state after PUT operations
	assert.Equal(t, "1.2.0", metadata["version"])    // Updated in second update
	assert.Equal(t, "test", metadata["env"])         // Preserved through updates
	assert.Equal(t, "feature-a", metadata["branch"]) // Preserved through updates
	assert.Equal(t, "abc123", metadata["build_id"])  // From second update
	assert.Equal(t, "true", metadata["deployed"])    // From second update

	// Should have exactly the keys from the last PUT: version, env, branch, build_id, deployed
	assert.Len(t, metadata, 5)
}

func TestPausedSandboxMetadataUpdate(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox with initial metadata
	initialMetadata := api.SandboxMetadata{
		"sandboxType": "test",
		"version":     "1.0.0",
		"environment": "paused-test",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c, utils.WithMetadata(initialMetadata))

	// Verify initial metadata exists
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)
	assert.NotNil(t, getSandboxResponse.JSON200.Metadata)
	assert.Equal(t, "test", (*getSandboxResponse.JSON200.Metadata)["sandboxType"])
	assert.Equal(t, "1.0.0", (*getSandboxResponse.JSON200.Metadata)["version"])
	assert.Equal(t, "paused-test", (*getSandboxResponse.JSON200.Metadata)["environment"])

	// Pause the sandbox
	pauseResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResponse.StatusCode())

	// Update metadata while paused
	updateMetadata := api.SandboxMetadata{
		"environment": "paused-updated", // Update existing key
		"version":     "2.0.0",          // Update existing key
		"state":       "paused",         // Add new key
		// Note: "sandboxType" key is not included, will be removed with PUT
	}

	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(), sandbox.SandboxID, updateMetadata, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, updateResponse.StatusCode())

	// Verify metadata
	pausedSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pausedSandboxResponse.StatusCode())
	require.NotNil(t, pausedSandboxResponse.JSON200)
	require.NotNil(t, pausedSandboxResponse.JSON200.Metadata)

	pausedMeta := *pausedSandboxResponse.JSON200.Metadata

	// Verify updated values persists
	assert.Equal(t, "paused-updated", pausedMeta["environment"])
	assert.Equal(t, "2.0.0", pausedMeta["version"])
	assert.Equal(t, "paused", pausedMeta["state"])

	// Resume the sandbox to verify metadata persisted
	resumeRequest := api.PostSandboxesSandboxIDResumeJSONRequestBody{}
	resumeResponse, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sandbox.SandboxID, resumeRequest, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resumeResponse.StatusCode())

	// Verify metadata after resume
	getResumedSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getResumedSandboxResponse.StatusCode())
	require.NotNil(t, getResumedSandboxResponse.JSON200)
	require.NotNil(t, getResumedSandboxResponse.JSON200.Metadata)

	resumedMeta := *getResumedSandboxResponse.JSON200.Metadata

	// Verify updated values persist after resume
	assert.Equal(t, "paused-updated", resumedMeta["environment"])
	assert.Equal(t, "2.0.0", resumedMeta["version"])
	assert.Equal(t, "paused", resumedMeta["state"])

	// Verify sandboxType was removed
	_, hasSandboxType := resumedMeta["sandboxType"]
	assert.False(t, hasSandboxType, "sandboxType should be removed as it was not in PUT request")

	// Verify we have only the keys from PUT request
	assert.Len(t, resumedMeta, 3)
}
