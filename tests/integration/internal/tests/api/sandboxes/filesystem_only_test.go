package sandboxes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// pauseFilesystemOnly pauses the sandbox as a filesystem-only snapshot
// (memory:false): only the rootfs is persisted, so resuming it cold-boots
// (reboots) the guest instead of restoring memory.
func pauseFilesystemOnly(t *testing.T, c *api.ClientWithResponses, sandboxID string) {
	t.Helper()

	memory := false
	resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sandboxID,
		api.PostSandboxesSandboxIDPauseJSONRequestBody{Memory: &memory}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode(), "filesystem-only pause should succeed")
}

// TestSandboxConnect_FilesystemOnlyRefused verifies that POST /connect refuses a
// filesystem-only snapshot. Connecting implicitly resumes, which for a disk-only
// snapshot means a cold boot that loses in-memory state — breaking connect's
// "same sandbox" contract — so the API must return 409 and require an explicit
// resume instead.
func TestSandboxConnect_FilesystemOnlyRefused(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

	pauseFilesystemOnly(t, c, sbx.SandboxID)

	connectResp, err := c.PostSandboxesSandboxIDConnectWithResponse(t.Context(), sbx.SandboxID,
		api.PostSandboxesSandboxIDConnectJSONRequestBody{Timeout: 30}, setup.WithAPIKey())
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, connectResp.StatusCode(),
		"connecting to a filesystem-only snapshot must be refused with 409")
}
