package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestInternetAccess(t *testing.T) {
	ctx := t.Context()
	sbxTimeout := int32(30)

	client := setup.GetAPIClient()

	testCases := []struct {
		name           string
		internetAccess bool
	}{
		{
			name:           "allow_internet_access",
			internetAccess: true,
		},
		{
			name:           "deny_internet_access",
			internetAccess: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp, err := client.PostSandboxesWithResponse(ctx, api.NewSandbox{
				TemplateID:          setup.SandboxTemplateID,
				Timeout:             &sbxTimeout,
				AllowInternetAccess: &tc.internetAccess,
			}, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, resp.StatusCode(), http.StatusCreated, "Expected status code 201 Created, got %d", resp.StatusCode())
			require.NotNil(t, resp.JSON201, "Expected non-nil response body")

			envdClient := setup.GetEnvdClient(t, ctx)

			err = utils.ExecCommand(t, ctx, resp.JSON201, envdClient, "curl", "--connect-timeout=3", "--max-time=5", "-Is", "https://www.google.com")
			if tc.internetAccess {
				require.NoError(t, err, "Expected curl command to succeed when internet access is allowed")
			} else {
				require.Error(t, err, "Expected curl command to fail when internet access is denied")
				require.Contains(t, err.Error(), "curl: (7) Failed to connect", "Expected connection failure message")
			}
		})
	}
}

func TestInternetAccessResumedSbx(t *testing.T) {
	ctx := t.Context()
	sbxTimeout := int32(30)

	client := setup.GetAPIClient()

	testCases := []struct {
		name           string
		internetAccess bool
	}{
		{
			name:           "allow_internet_access",
			internetAccess: true,
		},
		{
			name:           "deny_internet_access",
			internetAccess: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp, err := client.PostSandboxesWithResponse(ctx, api.NewSandbox{
				TemplateID:          setup.SandboxTemplateID,
				Timeout:             &sbxTimeout,
				AllowInternetAccess: &tc.internetAccess,
			}, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, resp.StatusCode(), http.StatusCreated, "Expected status code 201 Created, got %d", resp.StatusCode())
			require.NotNil(t, resp.JSON201, "Expected non-nil response body")

			// Pause and resume the sandbox
			_, err = client.PostSandboxesSandboxIDPauseWithResponse(ctx, resp.JSON201.SandboxID, setup.WithAPIKey())
			require.NoError(t, err, "Expected to pause sandbox without error")
			_, err = client.PostSandboxesSandboxIDResumeWithResponse(ctx, resp.JSON201.SandboxID, api.PostSandboxesSandboxIDResumeJSONRequestBody{
				Timeout: &sbxTimeout,
			})
			require.NoError(t, err, "Expected to resume sandbox without error")
			envdClient := setup.GetEnvdClient(t, ctx)

			err = utils.ExecCommand(t, ctx, resp.JSON201, envdClient, "curl", "--connect-timeout=3", "--max-time=5", "-Is", "https://www.google.com")
			if tc.internetAccess {
				require.NoError(t, err, "Expected curl command to succeed when internet access is allowed")
			} else {
				require.Error(t, err, "Expected curl command to fail when internet access is denied")
				require.Contains(t, err.Error(), "curl: (7) Failed to connect", "Expected connection failure message")
			}
		})
	}
}
