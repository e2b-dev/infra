package utils

import (
	"context"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

type SandboxConfig struct {
	templateID          string
	metadata            api.SandboxMetadata
	timeout             int32
	autoPause           bool
	autoResume          *api.SandboxAutoResumeConfig
	network             *api.SandboxNetworkConfig
	allowInternetAccess *bool
	secure              *bool
}

type SandboxOption func(config *SandboxConfig)

func WithMetadata(metadata api.SandboxMetadata) SandboxOption {
	return func(config *SandboxConfig) {
		maps.Copy(config.metadata, metadata)
	}
}

func WithoutAnyMetadata() SandboxOption {
	return func(config *SandboxConfig) {
		config.metadata = make(map[string]string)
	}
}

func WithTimeout(timeout int32) SandboxOption {
	return func(config *SandboxConfig) {
		config.timeout = timeout
	}
}

func WithAutoPause(autoPause bool) SandboxOption {
	return func(config *SandboxConfig) {
		config.autoPause = autoPause
	}
}

func WithAutoResume(policy api.SandboxAutoResumePolicy) SandboxOption {
	return func(config *SandboxConfig) {
		config.autoResume = &api.SandboxAutoResumeConfig{Policy: &policy}
	}
}

func WithSecure(secure bool) SandboxOption {
	return func(config *SandboxConfig) {
		config.secure = &secure
	}
}

func WithNetwork(network *api.SandboxNetworkConfig) SandboxOption {
	return func(config *SandboxConfig) {
		config.network = network
	}
}

func WithAllowInternetAccess(allow bool) SandboxOption {
	return func(config *SandboxConfig) {
		config.allowInternetAccess = &allow
	}
}

func WithTemplateID(templateID string) SandboxOption {
	return func(config *SandboxConfig) {
		config.templateID = templateID
	}
}

// SetupSandboxWithCleanup creates a new sandbox and returns its data
func SetupSandboxWithCleanup(t *testing.T, c *api.ClientWithResponses, options ...SandboxOption) *api.Sandbox {
	t.Helper()

	// t.Context() doesn't work with go vet, so we use our own context
	ctx, cancel := context.WithCancel(t.Context())
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

	templateID := config.templateID
	if templateID == "" {
		templateID = setup.SandboxTemplateID
	}

	for range 10 { // retry up to 10 times, but only in case of 429
		createSandboxResponse, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
			TemplateID:          templateID,
			Timeout:             &config.timeout,
			Metadata:            &config.metadata,
			AutoPause:           &config.autoPause,
			AutoResume:          config.autoResume,
			Network:             config.network,
			AllowInternetAccess: config.allowInternetAccess,
			Secure:              config.secure,
		}, setup.WithAPIKey())
		require.NoError(t, err)

		if createSandboxResponse.StatusCode() == http.StatusTooManyRequests {
			t.Logf("Sandbox creation failed with status code %d, retrying...", createSandboxResponse.StatusCode())
			time.Sleep(time.Second * 5)

			continue
		}

		if createSandboxResponse.StatusCode() != http.StatusCreated {
			t.Logf("Sandbox creation failed status=%d body=%s", createSandboxResponse.StatusCode(), string(createSandboxResponse.Body))
			if createSandboxResponse.JSON400 != nil {
				t.Logf("Sandbox creation JSON400=%+v", *createSandboxResponse.JSON400)
			}
		}

		require.Equal(t, http.StatusCreated, createSandboxResponse.StatusCode())
		sbx := createSandboxResponse.JSON201
		require.NotNil(t, sbx)

		t.Cleanup(func() {
			TeardownSandbox(t, c, sbx.SandboxID)
		})

		return sbx
	}

	t.Logf("Sandbox creation failed after 10 retries")
	t.FailNow()

	return nil
}

// TeardownSandbox kills the sandbox with the given ID
func TeardownSandbox(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	t.Helper()

	ctx := context.WithoutCancel(t.Context())

	killSandboxResponse, err := c.DeleteSandboxesSandboxIDWithResponse(ctx, sandboxID, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Contains(t, []int{http.StatusNoContent, http.StatusNotFound}, killSandboxResponse.StatusCode())
}
