package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

// setupSandbox creates a new sandbox and returns its ID
func setupSandbox(t *testing.T, c *api.ClientWithResponses) string {
	createSandboxResponse, err := c.PostSandboxesWithResponse(context.Background(), api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
	}, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

	return createSandboxResponse.JSON201.SandboxID
}

// teardownSandbox kills the sandbox with the given ID
func teardownSandbox(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())
}

func TestSandboxList(t *testing.T) {
	c := setup.GetAPIClient(t)

	t.Run("list running sandboxes", func(t *testing.T) {
		// Setup: create a new sandbox
		sandboxID := setupSandbox(t, c)
		// Teardown: ensure the sandbox is killed when the test finishes
		defer teardownSandbox(t, c, sandboxID)

		// list all sandboxes and check that the sandbox is in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{
			State: &[]api.SandboxState{api.Running},
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, listSandboxesResponse.StatusCode())

		runningSandboxes := listSandboxesResponse.JSON200
		if runningSandboxes == nil {
			t.Fatalf("no sandboxes found")
		}

		assert.Contains(t, *runningSandboxes, sandboxID)
	})

	t.Run("list paused sandboxes", func(t *testing.T) {
		// Setup: create a new sandbox
		sandboxID := setupSandbox(t, c)
		// Teardown: ensure the sandbox is killed when the test finishes
		defer teardownSandbox(t, c, sandboxID)

		// pause the sandbox
		pauseSandboxResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, pauseSandboxResponse.StatusCode())

		// list all paused sandboxes and check that the sandbox is in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{
			State: &[]api.SandboxState{api.Paused},
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, listSandboxesResponse.StatusCode())

		pausedSandboxes := listSandboxesResponse.JSON200
		if pausedSandboxes == nil {
			t.Fatalf("no paused sandboxes found")
		}

		assert.Contains(t, *pausedSandboxes, sandboxID)
	})

	t.Run("paginate sandboxes", func(t *testing.T) {
		// Setup: create two sandboxes
		sandboxID := setupSandbox(t, c)
		sandboxID2 := setupSandbox(t, c)

		// Teardown: ensure both sandboxes are killed when the test finishes
		defer teardownSandbox(t, c, sandboxID)
		defer teardownSandbox(t, c, sandboxID2)

		// list the sandboxes
		limit := int32(1)
		listSandboxesResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{
			Limit: &limit,
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, listSandboxesResponse.StatusCode())

		sandboxes := listSandboxesResponse.JSON200
		if sandboxes == nil {
			t.Fatalf("no sandboxes found")
		}

		assert.Equal(t, 1, len(*sandboxes))
		assert.Contains(t, *sandboxes, sandboxID)

		// get the next page token from headers
		nextPage := listSandboxesResponse.HTTPResponse.Header.Get("X-Next-Token")
		if nextPage == "" {
			t.Fatalf("no next page token found")
		}

		// get the next page
		nextPageResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{
			Limit:     &limit,
			NextToken: &nextPage,
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, nextPageResponse.StatusCode())

		nextPageSandboxes := nextPageResponse.JSON200
		if nextPageSandboxes == nil {
			t.Fatalf("no sandboxes found")
		}

		assert.Equal(t, 1, len(*nextPageSandboxes))
		assert.Contains(t, *nextPageSandboxes, sandboxID2)
	})
}
