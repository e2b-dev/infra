//go:build compression

package sandboxes

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// Compressed variants of sandbox tests.
// These run only with -tags compression and exercise the same logic
// as the untagged tests, but against an orchestrator with compression enabled.

func TestCompressPauseResume(t *testing.T)    { TestSandboxPause(t) }
func TestCompressSnapshotCreate(t *testing.T) { TestSnapshotTemplateCreate(t) }

// TestCompressLargeMemoryPauseResume fills ~200MB with 4x-compressible data,
// pauses, resumes, and verifies SHA-256 hash integrity.
// This is a stress test for the compressed read/write path — no untagged equivalent.
func TestCompressLargeMemoryPauseResume(t *testing.T) {
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
		fmt.Sprintf("Data integrity failed: before=%s, after=%s", hashBefore, hashAfter))
}
