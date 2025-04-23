package api

import (
	"context"
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
