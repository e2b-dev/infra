package utils

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

// SetupSandboxWithCleanup creates a new sandbox and returns its data
func SetupSandboxWithCleanup(t *testing.T, c *api.ClientWithResponses) *api.Sandbox {
	sbxTimeout := int32(30)
	createSandboxResponse, err := c.PostSandboxesWithResponse(t.Context(), api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
		Metadata: &api.SandboxMetadata{
			"sandboxType": "test",
		},
	}, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())
	assert.NotNil(t, createSandboxResponse.JSON201)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(createSandboxResponse.Body))
		}
		TeardownSandbox(t, c, createSandboxResponse.JSON201.SandboxID)
	})

	return createSandboxResponse.JSON201
}

// TeardownSandbox kills the sandbox with the given ID
func TeardownSandbox(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())
}
