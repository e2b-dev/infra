package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestCreateAPIKey(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Create the API key
	resp, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
		Name: "test",
	}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
	assert.Equal(t, "test", resp.JSON201.Name)
	assert.NotEmpty(t, resp.JSON201.Key)
	assert.Regexp(t, fmt.Sprintf("^%s.+$", keys.ApiKeyPrefix), resp.JSON201.Key)
}

func TestCreateAPIKeyForeignTeam(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	// Create first team and API key
	foreignTeamID := utils.CreateTeam(t, db, "test-team-apikey-foreign")

	// Create the API key
	resp, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
		Name: "foreign",
	}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, foreignTeamID.String()))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode(), "Expected 401 Unauthorized when creating API key for a foreign team")
}

func TestCreateAPIKeyForeignTeamWithCache(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	// Create first team
	foreignUserID := utils.CreateUser(t, db)
	foreignTeamID := utils.CreateTeamWithUser(t, db, "test-team-apikey-foreign-cache", foreignUserID.String())

	// Populate cache by calling some endpoint
	utils.CreateAPIKey(t, ctx, c, foreignUserID.String(), foreignTeamID)

	// Create the API key in foreign team
	resp, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
		Name: "foreign-cached",
	}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, foreignTeamID.String()))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode(), "Expected 401 Unauthorized when creating API key for a foreign team")
}

func TestDeleteAPIKey(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	c := setup.GetAPIClient()

	t.Run("succeeds", func(t *testing.T) {
		t.Parallel()
		// Create the API key
		respC, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
			Name: "test",
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusCreated, respC.StatusCode())

		// Delete the API key
		respD, err := c.DeleteApiKeysApiKeyIDWithResponse(ctx, respC.JSON201.Id.String(), setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNoContent, respD.StatusCode())
	})

	t.Run("id does not exist", func(t *testing.T) {
		t.Parallel()
		respD, err := c.DeleteApiKeysApiKeyIDWithResponse(ctx, uuid.New().String(), setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respD.StatusCode())
	})

	t.Run("cant delete other teams api key", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		db := setup.GetTestDBClient(t)
		c := setup.GetAPIClient()

		// Create first team and API key
		teamID1 := utils.CreateTeamWithUser(t, db, "test-team-apikey-delete-1", setup.UserID)

		// Create second team and API key
		teamID2 := utils.CreateTeamWithUser(t, db, "test-team-apikey-delete-2", setup.UserID)

		// Create an additional API key for team1
		resp, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
			Name: fmt.Sprintf("test-delete-%s", teamID1),
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID1.String()))
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode())
		apiKeyID := resp.JSON201.Id

		// Try to delete team1's API key using team2's API key - should fail
		deleteResp, err := c.DeleteApiKeysApiKeyIDWithResponse(ctx, apiKeyID.String(), setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID2.String()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, deleteResp.StatusCode())

		// Verify the API key still exists for team1
		listResp, err := c.GetApiKeysWithResponse(ctx, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID1.String()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, listResp.StatusCode())

		found := false
		for _, key := range *listResp.JSON200 {
			if key.Id == apiKeyID {
				found = true

				break
			}
		}
		assert.True(t, found, "API key should still exist for team1")

		// Verify that team1 can delete their own API key
		deleteResp2, err := c.DeleteApiKeysApiKeyIDWithResponse(ctx, apiKeyID.String(), setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID1.String()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, deleteResp2.StatusCode())

		// Verify the API key was deleted
		listResp2, err := c.GetApiKeysWithResponse(ctx, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID1.String()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, listResp2.StatusCode())

		found = false
		for _, key := range *listResp2.JSON200 {
			if key.Id == apiKeyID {
				found = true

				break
			}
		}
		assert.False(t, found, "API key should be deleted from team1's list")
	})
}

func TestListAPIKeys(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	resp, err := c.GetApiKeysWithResponse(ctx, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, http.StatusOK, resp.StatusCode())
	assert.NotNil(t, resp.JSON200)
	assert.NotEmpty(t, *resp.JSON200)
}

func TestPatchAPIKey(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	c := setup.GetAPIClient()

	// Create the first API key
	respC, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
		Name: "test-patch-1",
	}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
	if err != nil {
		t.Fatal(err)
	}

	respList1, err := c.GetApiKeysWithResponse(ctx, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, http.StatusOK, respList1.StatusCode())

	// Extract names from API keys
	apiKeyNames := []string{}
	for _, key := range *respList1.JSON200 {
		apiKeyNames = append(apiKeyNames, key.Name)
	}
	assert.Contains(t, apiKeyNames, "test-patch-1")

	t.Run("succeeds", func(t *testing.T) {
		t.Parallel()
		// Rename the API key
		respP, err := c.PatchApiKeysApiKeyIDWithResponse(ctx, respC.JSON201.Id.String(), api.PatchApiKeysApiKeyIDJSONRequestBody{
			Name: "test-patch-2",
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(t, http.StatusAccepted, respP.StatusCode())

		respList2, err := c.GetApiKeysWithResponse(ctx, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, respList1.StatusCode())

		// Extract names from API keys
		apiKeyNames = []string{}
		for _, key := range *respList2.JSON200 {
			apiKeyNames = append(apiKeyNames, key.Name)
		}
		assert.Contains(t, apiKeyNames, "test-patch-2")
	})

	t.Run("id does not exist", func(t *testing.T) {
		t.Parallel()
		respP, err := c.PatchApiKeysApiKeyIDWithResponse(ctx, uuid.New().String(), api.PatchApiKeysApiKeyIDJSONRequestBody{
			Name: "test-patch-3",
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respP.StatusCode())
	})

	t.Run("cant patch other teams api keys", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		db := setup.GetTestDBClient(t)
		c := setup.GetAPIClient()

		// Create first team and API key
		teamID1 := utils.CreateTeamWithUser(t, db, "test-team-apikey-patch-1", setup.UserID)

		// Create second team and API key
		teamID2 := utils.CreateTeamWithUser(t, db, "test-team-apikey-patch-2", setup.UserID)

		// Create an additional API key for team1
		resp, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
			Name: fmt.Sprintf("test-patch-%s", teamID1),
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID1.String()))
		if err != nil {
			t.Fatal(err)
		}
		require.Equal(t, http.StatusCreated, resp.StatusCode())
		apiKeyID := resp.JSON201.Id

		// Try to patch team1's API key using team2's API key - should fail
		patchResp, err := c.PatchApiKeysApiKeyIDWithResponse(ctx, apiKeyID.String(), api.PatchApiKeysApiKeyIDJSONRequestBody{
			Name: "hacked-name",
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID2.String()))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, patchResp.StatusCode())

		// Verify that team1 can still patch their own API key
		patchResp2, err := c.PatchApiKeysApiKeyIDWithResponse(ctx, apiKeyID.String(), api.PatchApiKeysApiKeyIDJSONRequestBody{
			Name: "legitimate-update",
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID1.String()))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusAccepted, patchResp2.StatusCode())

		// Verify the API key was updated correctly
		listResp, err := c.GetApiKeysWithResponse(ctx, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID1.String()))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusOK, listResp.StatusCode())

		found := false
		for _, key := range *listResp.JSON200 {
			if key.Id == apiKeyID {
				assert.Equal(t, "legitimate-update", key.Name)
				found = true

				break
			}
		}
		assert.True(t, found, "API key should be found in team1's list")
	})
}

func TestAPIKeyLastUsedUpdated(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	// The last used is updated only once a minute
	expectedLastUsed := time.Now().Add(-2 * time.Minute)
	// Use the api key
	_, err := c.GetSandboxesWithResponse(t.Context(), nil, setup.WithAPIKey())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		resp, err := c.GetApiKeysWithResponse(t.Context(), setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode())
		require.NotNil(t, resp.JSON200)

		for _, key := range *resp.JSON200 {
			if strings.HasPrefix(setup.APIKey, fmt.Sprintf("%s%s", key.Mask.Prefix, key.Mask.MaskedValuePrefix)) && strings.HasSuffix(setup.APIKey, key.Mask.MaskedValueSuffix) {
				if key.LastUsed == nil {
					return false
				}

				// Last used should be recent
				if key.LastUsed.Before(expectedLastUsed) {
					return false
				}

				return true
			}
		}

		return false
	}, 10*time.Second, 50*time.Millisecond, "Expected API key last used to be updated")
}
