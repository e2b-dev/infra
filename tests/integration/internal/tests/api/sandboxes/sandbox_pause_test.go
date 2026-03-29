package sandboxes

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxPause(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	t.Run("regular pause", func(t *testing.T) {
		t.Parallel()
		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		// Set timeout to 0 to force sandbox to be stopped
		resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)
		assert.Equal(t, api.Paused, res.JSON200.State)

		// Resume the sandbox with auto-pause enabled
		sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, sbxResume.StatusCode())
		require.NotNil(t, sbxResume.JSON201)
		assert.Equal(t, sbxResume.JSON201.SandboxID, sbxId)
	})

	t.Run("test concurrent pauses", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
		sbxId := sbx.SandboxID

		// Pause the sandbox
		wg := errgroup.Group{}
		for range 5 {
			wg.Go(func() error {
				pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
				require.NoError(t, err)
				require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

				return nil
			})
		}

		err := wg.Wait()
		require.NoError(t, err)

		res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode())
		require.NotNil(t, res.JSON200)
		assert.Equal(t, api.Paused, res.JSON200.State)
	})

	t.Run("pause killed sandbox", func(t *testing.T) {
		t.Parallel()
		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(true))
		sbxId := sbx.SandboxID

		// Kill the sandbox
		killResp, err := c.DeleteSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, killResp.StatusCode())

		pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, pauseResp.StatusCode())
	})

	t.Run("pause already paused sandbox", func(t *testing.T) {
		t.Parallel()
		sbx := utils.SetupSandboxWithCleanup(t, c)
		sbxId := sbx.SandboxID

		// Pause the sandbox
		resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		// Try to pause the sandbox again
		resp, err = c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusConflict, resp.StatusCode())
	})
}

// TestLargeMemoryPauseResume fills ~200MB with 4x-compressible data,
// pauses, resumes, and verifies SHA-256 hash integrity.
// Exercises both memfile and rootfs paths under the active compression config.
func TestLargeMemoryPauseResume(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	ctx := t.Context()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

	// Disk (rootfs): 1 MB random + 3 MB zeros, repeated = 200 MB, ~4x compressible.
	// RAM (tmpfs): same pattern, 100 MB. Exercises both memfile and rootfs compression.
	fillScript := strings.Join([]string{
		`python3 -c "
import os
for path, n in [('/tmp/large_data', 200), ('/dev/shm/mem_data', 100)]:
    with open(path, 'wb') as f:
        for i in range(n):
            if i % 4 == 0:
                f.write(os.urandom(1<<20))
            else:
                f.write(b'\x00' * (1<<20))
"`,
		`sha256sum /tmp/large_data /dev/shm/mem_data | awk '{print $1}' | paste -sd, > /tmp/data_hash`,
		`du -sh /tmp/large_data /dev/shm/mem_data`,
	}, " && ")

	t.Log("Filling sandbox with compressible data...")
	output, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "root", "/bin/sh", "-c", fillScript)
	require.NoError(t, err, "failed to fill memory with test data")
	t.Logf("Data size: %s", strings.TrimSpace(output))

	hashBefore, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "user", "cat", "/tmp/data_hash")
	require.NoError(t, err)
	hashBefore = strings.TrimSpace(hashBefore)
	require.NotEmpty(t, hashBefore)
	t.Logf("SHA-256 before pause: %s", hashBefore)

	t.Log("Pausing...")
	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

	t.Log("Resuming...")
	resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resumeResp.StatusCode())

	hashAfterOutput, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "user", "/bin/sh", "-c", "sha256sum /tmp/large_data /dev/shm/mem_data | awk '{print $1}' | paste -sd,")
	require.NoError(t, err)
	hashAfter := strings.TrimSpace(hashAfterOutput)
	t.Logf("SHA-256 after resume: %s", hashAfter)

	require.Equal(t, hashBefore, hashAfter,
		"Data integrity failed: before=%s, after=%s", hashBefore, hashAfter)
}
