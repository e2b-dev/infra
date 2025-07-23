package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestSandboxKill(t *testing.T) {
	c := setup.GetAPIClient()

	t.Run("kill a non-existing sandbox", func(t *testing.T) {
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), "non-existing", setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, killSandboxResponse.StatusCode())
	})

	t.Run("start and kill a sandbox", func(t *testing.T) {
		// create a new samdbox
		createSandboxResponse, err := c.PostSandboxesWithResponse(t.Context(), api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
		}, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

		sandboxID := createSandboxResponse.JSON201.SandboxID

		// kill the sandbox
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())

		// list all sandboxes and check that the sandbox is not in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(t.Context(), &api.GetSandboxesParams{}, setup.WithAPIKey())

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
		createSandboxResponse, err := c.PostSandboxesWithResponse(t.Context(), api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
		}, setup.WithAPIKey())

		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

		sandboxID := createSandboxResponse.JSON201.SandboxID

		// pause the sandbox
		pauseSandboxResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, pauseSandboxResponse.StatusCode())

		// kill the sandbox
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())

		assert.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())

		// list all sandboxes and check that the sandbox is not in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(t.Context(), &api.GetSandboxesParams{}, setup.WithAPIKey())

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
		createSandboxResponse, err := c.PostSandboxesWithResponse(t.Context(), api.NewSandbox{
			TemplateID: setup.SandboxTemplateID,
		}, setup.WithAPIKey())

		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())

		sandboxID := createSandboxResponse.JSON201.SandboxID
		clientID := createSandboxResponse.JSON201.ClientID

		// pause the sandbox
		pauseSandboxResponse, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID, setup.WithAPIKey())

		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseSandboxResponse.StatusCode())

		// resume the sandbox
		timeout := int32(1000)
		resumeSandboxResponse, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sandboxID+"-"+clientID, api.PostSandboxesSandboxIDResumeJSONRequestBody{
			Timeout: &timeout,
		}, setup.WithAPIKey())

		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resumeSandboxResponse.StatusCode())

		// kill the sandbox
		killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())

		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())

		// list all sandboxes and check that the sandbox is not in the list
		listSandboxesResponse, err := c.GetSandboxesWithResponse(t.Context(), &api.GetSandboxesParams{}, setup.WithAPIKey())

		require.NoError(t, err)
		require.Equal(t, http.StatusOK, listSandboxesResponse.StatusCode())

		runningSandboxes := listSandboxesResponse.JSON200
		if runningSandboxes == nil {
			t.Fatalf("no sandboxes found")
		}

		assert.NotContains(t, *runningSandboxes, sandboxID)
	})
}
