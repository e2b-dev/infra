package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestCreateSandboxWithSecuredEnvd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(60)
	sbxSecure := true

	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
		Secure:     &sbxSecure,
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
	assert.NotNil(t, resp.JSON201.EnvdAccessToken)

	getResp, getErr := c.GetSandboxesSandboxIDWithResponse(ctx, resp.JSON201.SandboxID, setup.WithAPIKey())
	if getErr != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
	assert.Equal(t, *resp.JSON201.EnvdAccessToken, *getResp.JSON200.EnvdAccessToken)
}
