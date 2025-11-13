package orchestrator

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestEntropyDeviceAvailability(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	// Create a sandbox
	sbx := utils.SetupSandboxWithCleanup(t, c)

	envdClient := setup.GetEnvdClient(t, t.Context())

	t.Run("can read from hardware random device", func(t *testing.T) {
		// Try to read a small amount of random data from /dev/hwrng
		cmd := "sudo dd if=/dev/hwrng bs=1 count=10 2>/dev/null | wc -c"
		output, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", cmd)
		require.NoError(t, err)

		bytesRead, err := strconv.Atoi(strings.TrimSpace(output))
		require.NoError(t, err)
		assert.Equal(t, 10, bytesRead, "Should read exactly 10 bytes from /dev/hwrng")
	})

	t.Run("random data is non-zero", func(t *testing.T) {
		// Read some random bytes and verify they're not all zeros
		cmd := "sudo dd if=/dev/hwrng bs=1 count=100 2>/dev/null | od -An -tu1 | tr -s ' ' | grep -v '^$'"
		output, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", cmd)
		require.NoError(t, err)

		// Output should contain non-zero bytes
		assert.NotEmpty(t, output, "Should receive random data")
		// Check that not all bytes are zero
		assert.NotContains(t, output, strings.Repeat("0 ", 100), "Random data should not be all zeros")
	})
}

func TestEntropyDeviceAfterPauseResume(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	// Create a sandbox with auto-pause disabled
	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
	sbxID := sbx.SandboxID

	envdClient := setup.GetEnvdClient(t, t.Context())

	// Read some random data before pause
	readCmd := "sudo dd if=/dev/hwrng bs=1 count=100 2>/dev/null | md5sum | awk '{print $1}'"
	hash1, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", readCmd)
	require.NoError(t, err)
	hash1 = strings.TrimSpace(hash1)
	require.NotEmpty(t, hash1)

	// Pause the sandbox
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, 204, pauseResp.StatusCode())

	// Verify it's paused
	getResp, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, 200, getResp.StatusCode())
	require.NotNil(t, getResp.JSON200)
	assert.Equal(t, api.Paused, getResp.JSON200.State)

	// Resume the sandbox
	resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxID, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, 201, resumeResp.StatusCode())
	require.NotNil(t, resumeResp.JSON201)
	assert.Equal(t, sbxID, resumeResp.JSON201.SandboxID)

	// Read random data after resume
	hash2, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", readCmd)
	require.NoError(t, err)
	hash2 = strings.TrimSpace(hash2)
	require.NotEmpty(t, hash2)

	// The hashes should be different (different random data)
	assert.NotEqual(t, hash1, hash2, "Random data after resume should be different from before pause")

	// Verify we can still read a specific amount after resume
	verifyCmd := "sudo dd if=/dev/hwrng bs=1 count=50 2>/dev/null | wc -c"
	output, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", verifyCmd)
	require.NoError(t, err)
	bytesRead, err := strconv.Atoi(strings.TrimSpace(output))
	require.NoError(t, err)
	assert.Equal(t, 50, bytesRead, "Should be able to read exactly 50 bytes after resume")
}
