package api

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"

	"github.com/stretchr/testify/assert"
)

func TestSandboxCreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())

	if err != nil {
		t.Fatal(err)
	}

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxCreate, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{TemplateID: setup.SandboxTemplateID}, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	// try to generate non-existing sandbox id but with real client part
	unknownSbxId := "xxx" + sbxCreate.JSON201.SandboxID[3:] + "-" + sbxCreate.JSON201.ClientID

	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, unknownSbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		utils.TeardownSandbox(t, c, sbxCreate.JSON201.SandboxID)
	})

	assert.Equal(t, http.StatusNotFound, sbxResume.StatusCode())
	assert.Equal(t, "{\"code\":404,\"message\":\"Sandbox snapshot not found\"}", string(sbxResume.Body))
}

func TestSandboxResumeWithSecuredEnvd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxSecure := true
	sbxCreate, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{TemplateID: setup.SandboxTemplateID, Secure: &sbxSecure}, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusCreated, sbxCreate.StatusCode())
	require.NotNil(t, sbxCreate.JSON201)

	_, err = c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbxCreate.JSON201.SandboxID, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxCreate.JSON201.SandboxID, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusOK, sbxResume.StatusCode())
	require.NotNil(t, sbxResume.JSON201)

	t.Cleanup(func() {
		utils.TeardownSandbox(t, c, sbxCreate.JSON201.SandboxID)
	})

	assert.Equal(t, sbxResume.JSON201.SandboxID, sbxCreate.JSON201.SandboxID)
	assert.Equal(t, sbxResume.JSON201.EnvdAccessToken, sbxCreate.JSON201.EnvdAccessToken)
}
