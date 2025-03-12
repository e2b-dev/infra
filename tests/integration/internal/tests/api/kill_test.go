package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

func TestSandboxKill(t *testing.T) {
	c := setup.GetAPIClient()

	t.Run("kill a non-existing sandbox", func(t *testing.T) {
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), "non-existing", setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, killSandboxResponse.StatusCode())
	})

	t.Run("start and kill a sandbox", func(t *testing.T) {
		// create a new samdbox
		createSandboxResponse, err := c.PostSandboxesWithResponse(context.Background(), api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

		sandboxID := createSandboxResponse.JSON201.SandboxID

		// kill the sandbox
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())

		// list all sandboxes and check that the sandbox is not in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, listSandboxesResponse.StatusCode())

		runningSandboxes := listSandboxesResponse.JSON200
		if runningSandboxes == nil {
			t.Fatalf("no sandboxes found")
		}

		assert.NotContains(t, *runningSandboxes, sandboxID)
	})

	t.Run("start and kill a paused sandbox", func(t *testing.T) {
		// create a new sandbox
		createSandboxResponse, err := c.PostSandboxesWithResponse(context.Background(), api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

		sandboxID := createSandboxResponse.JSON201.SandboxID

		// pause the sandbox
		pauseSandboxResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, pauseSandboxResponse.StatusCode())

		// kill the sandbox
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())

		// list all sandboxes and check that the sandbox is not in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, listSandboxesResponse.StatusCode())

		runningSandboxes := listSandboxesResponse.JSON200
		if runningSandboxes == nil {
			t.Fatalf("no sandboxes found")
		}

		assert.NotContains(t, *runningSandboxes, sandboxID)
	})

	t.Run("start and kill a subsequently paused sandbox", func(t *testing.T) {
		// create a new sandbox
		createSandboxResponse, err := c.PostSandboxesWithResponse(context.Background(), api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

		sandboxID := createSandboxResponse.JSON201.SandboxID
		clientID := createSandboxResponse.JSON201.ClientID

		// pause the sandbox
		pauseSandboxResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, pauseSandboxResponse.StatusCode())

		// resume the sandbox
		timeout := int32(1000)
		resumeSandboxResponse, err := c.PostSandboxesSandboxIDResumeWithResponse(context.Background(), sandboxID+"-"+clientID, api.PostSandboxesSandboxIDResumeJSONRequestBody{
			Timeout: &timeout,
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resumeSandboxResponse.StatusCode())

		// kill the sandbox
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())

		// list all sandboxes and check that the sandbox is not in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(context.Background(), &api.GetSandboxesParams{}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, listSandboxesResponse.StatusCode())

		runningSandboxes := listSandboxesResponse.JSON200
		if runningSandboxes == nil {
			t.Fatalf("no sandboxes found")
		}

		assert.NotContains(t, *runningSandboxes, sandboxID)
	})
}
