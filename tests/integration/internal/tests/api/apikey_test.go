package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestCreateAPIKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	// Create the API key
	resp, err := c.PostApiKeysWithResponse(ctx, api.PostApiKeysJSONRequestBody{
		Name: "test",
	}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
	assert.Equal(t, "test", resp.JSON201.Name)
	assert.NotEmpty(t, resp.JSON201.Key)
	assert.Regexp(t, fmt.Sprintf("^%s.+$", keys.ApiKeyPrefix), resp.JSON201.Key)
}

func TestDeleteAPIKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	t.Run("succeeds", func(t *testing.T) {
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
		respD, err := c.DeleteApiKeysApiKeyIDWithResponse(ctx, uuid.New().String(), setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respD.StatusCode())
	})
}

func TestListAPIKeys(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		respP, err := c.PatchApiKeysApiKeyIDWithResponse(ctx, uuid.New().String(), api.PatchApiKeysApiKeyIDJSONRequestBody{
			Name: "test-patch-3",
		}, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, http.StatusNotFound, respP.StatusCode())
	})
}
