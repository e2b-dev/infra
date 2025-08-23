package api

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxMetadataOperations(t *testing.T) {
	tests := []struct {
		name             string
		initialMetadata  api.SandboxMetadata
		updateMetadata   api.SandboxMetadata
		expectedMetadata api.SandboxMetadata
		expectedStatus   int
		expectedKeyCount int
		description      string
	}{
		{
			name: "update_existing_and_add_new_keys",
			initialMetadata: api.SandboxMetadata{
				"sandboxType": "test",
				"version":     "1.0.0",
				"environment": "supr-cupr",
			},
			updateMetadata: api.SandboxMetadata{
				"environment": "e2b-is-place-to-be",
				"version":     "1.1.0",
				"branch":      "feature-test",
			},
			expectedMetadata: api.SandboxMetadata{
				"environment": "e2b-is-place-to-be",
				"version":     "1.1.0",
				"branch":      "feature-test",
			},
			expectedStatus:   http.StatusOK,
			expectedKeyCount: 3,
			description:      "PUT should replace all metadata, removing sandboxType and adding branch",
		},
		{
			name: "replace_with_empty_metadata",
			initialMetadata: api.SandboxMetadata{
				"test": "value",
				"foo":  "bar",
			},
			updateMetadata:   api.SandboxMetadata{},
			expectedMetadata: api.SandboxMetadata{},
			expectedStatus:   http.StatusOK,
			expectedKeyCount: 0,
			description:      "PUT with empty metadata should clear all existing metadata",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := setup.GetAPIClient()

			// Create sandbox with initial metadata
			sandbox := utils.SetupSandboxWithCleanup(t, c,
				utils.WithMetadata(tc.initialMetadata),
				utils.WithTimeout(60),
			)

			// Update metadata
			updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(),
				sandbox.SandboxID, tc.updateMetadata, setup.WithAPIKey())
			require.NoError(t, err)
			assert.Equal(t, tc.expectedStatus, updateResponse.StatusCode(), tc.description)

			// Verify updated metadata
			getResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(),
				sandbox.SandboxID, setup.WithAPIKey())
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, getResponse.StatusCode())
			require.NotNil(t, getResponse.JSON200)

			// Verify metadata content
			if tc.expectedKeyCount == 0 {
				if getResponse.JSON200.Metadata != nil {
					metadata := *getResponse.JSON200.Metadata
					assert.Empty(t, metadata, "Metadata should be empty")
				}
			} else {
				require.NotNil(t, getResponse.JSON200.Metadata)
				metadata := *getResponse.JSON200.Metadata
				assert.Len(t, metadata, tc.expectedKeyCount)

				// Verify expected values
				for key, expectedValue := range tc.expectedMetadata {
					actualValue, exists := metadata[key]
					assert.True(t, exists, "Key %s should exist", key)
					assert.Equal(t, expectedValue, actualValue, "Value for key %s should match", key)
				}

				// Verify removed keys
				for key := range tc.initialMetadata {
					if _, shouldExist := tc.expectedMetadata[key]; !shouldExist {
						_, exists := metadata[key]
						assert.False(t, exists, "Key %s should have been removed", key)
					}
				}
			}
		})
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

func TestSandboxMetadataErrors(t *testing.T) {
	testCases := []struct {
		name           string
		setupFunc      func(t *testing.T) (sandboxID string, apiKey func(*api.RequestEditorFn) api.RequestEditorFn)
		updateMetadata api.SandboxMetadata
		expectedStatus int
		description    string
	}{
		{
			name: "non_existent_sandbox",
			setupFunc: func(t *testing.T) (string, func(*api.RequestEditorFn) api.RequestEditorFn) {
				return "nonexistent-sandbox-id", func(fn *api.RequestEditorFn) api.RequestEditorFn {
					return setup.WithAPIKey()
				}
			},
			updateMetadata: api.SandboxMetadata{"test": "value"},
			expectedStatus: http.StatusNotFound,
			description:    "Non-existent sandbox should return 404",
		},
		{
			name: "missing_api_key",
			setupFunc: func(t *testing.T) (string, func(*api.RequestEditorFn) api.RequestEditorFn) {
				c := setup.GetAPIClient()
				sandbox := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(30))
				return sandbox.SandboxID, func(fn *api.RequestEditorFn) api.RequestEditorFn {
					return nil // No API key
				}
			},
			updateMetadata: api.SandboxMetadata{"test": "value"},
			expectedStatus: http.StatusUnauthorized,
			description:    "Missing API key should return 401",
		},
		{
			name: "invalid_api_key",
			setupFunc: func(t *testing.T) (string, func(*api.RequestEditorFn) api.RequestEditorFn) {
				c := setup.GetAPIClient()
				sandbox := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(30))
				return sandbox.SandboxID, func(fn *api.RequestEditorFn) api.RequestEditorFn {
					return setup.WithAPIKey("invalid-api-key")
				}
			},
			updateMetadata: api.SandboxMetadata{"test": "value"},
			expectedStatus: http.StatusUnauthorized,
			description:    "Invalid API key should return 401",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := setup.GetAPIClient()
			sandboxID, apiKeyFunc := tc.setupFunc(t)

			var updateResponse *api.PutSandboxesSandboxIDMetadataResponse
			var err error

			if apiKeyFunc(nil) == nil {
				// No API key case
				updateResponse, err = c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(),
					sandboxID, tc.updateMetadata)
			} else {
				// With API key
				updateResponse, err = c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(),
					sandboxID, tc.updateMetadata, apiKeyFunc(nil))
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectedStatus, updateResponse.StatusCode(), tc.description)
		})
	}
}

func TestSandboxMetadataCrossTeamAccess(t *testing.T) {
	ctx := t.Context()
	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	// Create team B with its own user and API key
	teamBUserID := utils.CreateUser(t, db)
	teamBID := utils.CreateTeamWithUser(t, c, db, "test-team-metadata-cross", teamBUserID.String())
	teamBAPIKey := utils.CreateAPIKey(t, ctx, c, teamBUserID.String(), teamBID)

	// Create sandbox with team A (default team)
	sandboxA := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(api.SandboxMetadata{"owner": "teamA", "sensitive": "data"}),
		utils.WithTimeout(30))

	// Attempt to update team A's sandbox with team B's credentials
	updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(ctx,
		sandboxA.SandboxID, api.SandboxMetadata{"owner": "teamB", "hacked": "true"}, setup.WithAPIKey(teamBAPIKey))

	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, updateResponse.StatusCode(),
		"Cross-team access should return 404 to avoid information disclosure")

	// Verify original metadata unchanged
	verifyResponse, err := c.GetSandboxesSandboxIDWithResponse(ctx,
		sandboxA.SandboxID, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, verifyResponse.StatusCode())
	require.NotNil(t, verifyResponse.JSON200)
	require.NotNil(t, verifyResponse.JSON200.Metadata)

	metadata := *verifyResponse.JSON200.Metadata
	assert.Equal(t, "teamA", metadata["owner"],
		"Original metadata should be unchanged")

	_, hasHacked := metadata["hacked"]
	assert.False(t, hasHacked,
		"Malicious metadata should not have been added")
}

func TestSandboxMetadataSequentialUpdates(t *testing.T) {
	c := setup.GetAPIClient()

	// Create sandbox with initial metadata
	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(api.SandboxMetadata{
			"version": "1.0.0",
			"env":     "test",
		}),
		utils.WithTimeout(60),
	)

	updates := []api.SandboxMetadata{
		{
			"version": "1.1.0",
			"env":     "test",
			"branch":  "feature-a",
		},
		{
			"version":  "1.2.0",
			"env":      "staging",
			"branch":   "feature-b",
			"build_id": "abc123",
		},
		{
			"version":  "2.0.0",
			"env":      "production",
			"deployed": "true",
		},
	}

	// Apply updates sequentially
	for i, update := range updates {
		updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(),
			sandbox.SandboxID, update, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, updateResponse.StatusCode(),
			"Update %d should succeed", i+1)
	}

	// Verify final state
	getResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(),
		sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getResponse.StatusCode())
	require.NotNil(t, getResponse.JSON200)
	require.NotNil(t, getResponse.JSON200.Metadata)

	metadata := *getResponse.JSON200.Metadata
	assert.Len(t, metadata, 3)

	expectedFinal := api.SandboxMetadata{
		"version":  "2.0.0",
		"env":      "production",
		"deployed": "true",
	}
	for key, expectedValue := range expectedFinal {
		assert.Equal(t, expectedValue, metadata[key],
			"Final value for key %s should match", key)
	}
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

func TestSandboxMetadataRaceCondition(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox with initial metadata
	initialMetadata := api.SandboxMetadata{
		"shared_counter": "0",
	}

	sandbox := utils.SetupSandboxWithCleanup(t, c,
		utils.WithMetadata(initialMetadata),
		utils.WithTimeout(60),
	)

	// Number of concurrent increments
	numIncrements := 20
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	// Each goroutine will try to increment the counter
	for i := 0; i < numIncrements; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			// Since we're using PUT (replace), each update will completely replace metadata
			// This simulates a race condition where updates may overwrite each other
			updateMetadata := api.SandboxMetadata{
				"shared_counter": fmt.Sprintf("%d", goroutineID),
				"updater_id":     fmt.Sprintf("goroutine-%d", goroutineID),
				"update_time":    time.Now().Format(time.RFC3339Nano),
			}

			updateResponse, err := c.PutSandboxesSandboxIDMetadataWithResponse(t.Context(),
				sandbox.SandboxID, updateMetadata, setup.WithAPIKey())

			if err == nil && updateResponse.StatusCode() == http.StatusOK {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	for i := 0; i < numIncrements; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(),
				sandbox.SandboxID, setup.WithAPIKey())

			require.NoError(t, err)
			require.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
			require.NotNil(t, getSandboxResponse.JSON200)
			require.NotNil(t, getSandboxResponse.JSON200.Metadata)

			metadata := *getSandboxResponse.JSON200.Metadata

			// Final state should be consistent (all fields from the same update)
			counter := metadata["shared_counter"]
			updaterID := metadata["updater_id"]
			updateTime := metadata["update_time"]

			// Verify consistency - updater_id should match the counter value
			expectedUpdaterID := fmt.Sprintf("goroutine-%s", counter)
			assert.Equal(t, expectedUpdaterID, updaterID, "Metadata should be internally consistent")

			// Verify update_time exists and is valid
			assert.NotEmpty(t, updateTime, "update_time should exist")

			// Verify we have exactly the expected keys
			assert.Len(t, metadata, 3, "Should have exactly 3 keys from the last update")
		}(i)
	}

	// Wait for all updates to complete
	wg.Wait()

	// All updates should succeed (no errors due to concurrent access)
	assert.Equal(t, numIncrements, successCount, "All concurrent updates should succeed")

	// Verify final state - should be from one of the updates
	getSandboxResponse, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandbox.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getSandboxResponse.StatusCode())
	require.NotNil(t, getSandboxResponse.JSON200)
	require.NotNil(t, getSandboxResponse.JSON200.Metadata)

	metadata := *getSandboxResponse.JSON200.Metadata

	// Final state should be consistent (all fields from the same update)
	counter := metadata["shared_counter"]
	updaterID := metadata["updater_id"]
	updateTime := metadata["update_time"]

	// Verify consistency - updater_id should match the counter value
	expectedUpdaterID := fmt.Sprintf("goroutine-%s", counter)
	assert.Equal(t, expectedUpdaterID, updaterID, "Metadata should be internally consistent")

	// Verify update_time exists and is valid
	assert.NotEmpty(t, updateTime, "update_time should exist")

	// Verify we have exactly the expected keys
	assert.Len(t, metadata, 3, "Should have exactly 3 keys from the last update")
}
