package envd

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestAccessingHyperloopServerViaIP(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(120))

	envdClient := setup.GetEnvdClient(t, ctx)

	err := utils.ExecCommand(t, ctx, sbx, envdClient, "/bin/bash", "-c", "curl -o output.txt http://192.0.2.1/me")
	require.NoError(t, err, "Should be able to contact hyperloop server")

	readPath := "output.txt"
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{Path: &readPath, Username: "user"},
		setup.WithSandbox(sbx.SandboxID),
	)

	require.NoError(t, readErr)
	assert.Equal(t, http.StatusOK, readRes.StatusCode())
	assert.JSONEq(t, fmt.Sprintf("{\"sandboxID\": \"%s\"}", sbx.SandboxID), string(readRes.Body))
}

func TestAccessingHyperloopServerViaDomain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithTimeout(120))

	envdClient := setup.GetEnvdClient(t, ctx)

	err := utils.ExecCommand(t, ctx, sbx, envdClient, "/bin/bash", "-c", "curl -o output.txt http://events.e2b.local/me")
	require.NoError(t, err, "Should be able to contact hyperloop server")

	readPath := "output.txt"
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{Path: &readPath, Username: "user"},
		setup.WithSandbox(sbx.SandboxID),
	)

	require.NoError(t, readErr)
	assert.Equal(t, http.StatusOK, readRes.StatusCode())
	assert.JSONEq(t, fmt.Sprintf("{\"sandboxID\": \"%s\"}", sbx.SandboxID), string(readRes.Body))
}
