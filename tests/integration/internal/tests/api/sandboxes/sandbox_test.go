package sandboxes

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

func TestSandboxCreate(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}

		if resp.JSON201 != nil {
			utils.TeardownSandbox(t, c, resp.JSON201.SandboxID)
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
}

func TestSandboxResumeUnknownSandbox(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	sbxCreate, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{TemplateID: setup.SandboxTemplateID}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, sbxCreate.StatusCode())
	require.NotNil(t, sbxCreate.JSON201)

	// try to generate non-existing sandbox id but with real client part
	unknownSbxId := "xxx" + sbxCreate.JSON201.SandboxID[3:] + "-" + sbxCreate.JSON201.ClientID

	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, unknownSbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		utils.TeardownSandbox(t, c, sbxCreate.JSON201.SandboxID)
	})

	assert.Equal(t, http.StatusNotFound, sbxResume.StatusCode())
	assert.JSONEq(t, "{\"code\":404,\"message\":\"Sandbox can't be resumed, no snapshot found\"}", string(sbxResume.Body))
}

func TestSandboxResumeWithSecuredEnvd(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	sbxSecure := true
	sbxCreate, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{TemplateID: setup.SandboxTemplateID, Secure: &sbxSecure}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, sbxCreate.StatusCode())
	require.NotNil(t, sbxCreate.JSON201)

	_, err = c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbxCreate.JSON201.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)

	sbxIdWithClient := sbxCreate.JSON201.SandboxID + "-" + sbxCreate.JSON201.ClientID

	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxIdWithClient, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)

	t.Cleanup(func() {
		utils.TeardownSandbox(t, c, sbxCreate.JSON201.SandboxID)
	})

	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxCreate.JSON201.SandboxID)
	assert.Equal(t, sbxResume.JSON201.EnvdAccessToken, sbxCreate.JSON201.EnvdAccessToken)
}

func TestSandboxPauseNonFound(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	r, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, "not-found", setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotFound, r.StatusCode())
	require.NotNil(t, r.JSON404)
}
