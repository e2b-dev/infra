package utils

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

// SetupSandboxWithCleanupWithTimeout creates a new sandbox with specific timeout and returns its data
func SetupSandboxWithCleanupWithTimeout(t *testing.T, c *api.ClientWithResponses, sbxTimeout int32) *api.Sandbox {
	t.Helper()

	// t.Context() doesn't work with go vet, so we use our own context
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	createSandboxResponse, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
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

// SetupSandboxWithCleanup creates a new sandbox and returns its data
func SetupSandboxWithCleanup(t *testing.T, c *api.ClientWithResponses) *api.Sandbox {
	return SetupSandboxWithCleanupWithTimeout(t, c, 30)
}

// TeardownSandbox kills the sandbox with the given ID
func TeardownSandbox(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	t.Helper()
	killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())
}
