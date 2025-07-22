package utils

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

type SandboxConfig struct {
	metadata api.SandboxMetadata
	timeout  int32
}

type SandboxOption func(config *SandboxConfig)

func WithMetadata(metadata api.SandboxMetadata) SandboxOption {
	return func(config *SandboxConfig) {
		for key, value := range metadata {
			config.metadata[key] = value
		}
	}
}

func WithTimeout(timeout int32) SandboxOption {
	return func(config *SandboxConfig) {
		config.timeout = timeout
	}
}

// SetupSandboxWithCleanup creates a new sandbox and returns its data
func SetupSandboxWithCleanup(t *testing.T, c *api.ClientWithResponses, options ...SandboxOption) *api.Sandbox {
	t.Helper()

	// t.Context() doesn't work with go vet, so we use our own context
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	config := SandboxConfig{
		timeout: 30, // default timeout
		metadata: api.SandboxMetadata{
			"sandboxType": "test",
		},
	}

	for _, option := range options {
		option(&config)
	}

	createSandboxResponse, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &config.timeout,
		Metadata:   &config.metadata,
	}, setup.WithAPIKey())

	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())
	require.NotNil(t, createSandboxResponse.JSON201)

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
	t.Helper()
	killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(context.Background(), sandboxID, setup.WithAPIKey())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, killSandboxResponse.StatusCode())
}
