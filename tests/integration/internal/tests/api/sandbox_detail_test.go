package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxDetailRunning(t *testing.T) {
	c := setup.GetAPIClient()

	// Create a sandbox for testing
	sbx := utils.SetupSandboxWithCleanup(t, c)

	// Test basic list functionality
	response, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())

	returnedSbx := response.JSON200
	assert.Equal(t, sbx.SandboxID, returnedSbx.SandboxID)
	assert.Equal(t, sbx.TemplateID, returnedSbx.TemplateID)
}

func TestSandboxDetailPaused(t *testing.T) {
	c := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, c)
	sandboxID := sbx.SandboxID
	pauseSandbox(t, c, sandboxID)

	response, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sandboxID, setup.WithAPIKey())

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	returnedSbx := response.JSON200
	assert.Equal(t, sbx.SandboxID, returnedSbx.SandboxID)
}
