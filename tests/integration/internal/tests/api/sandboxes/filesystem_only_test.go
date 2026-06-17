package sandboxes

import (
	"net/http"
	"strings"
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

// TestSandboxResume_FilesystemOnlyReboots verifies the filesystem-only happy
// path: an explicit resume of a disk-only snapshot is allowed and cold-boots
// (reboots) the guest. The rootfs must survive the reboot (a marker written
// before the pause is still there), while a fresh kernel boot id proves the
// guest was rebooted rather than restored from a memory snapshot.
func TestSandboxResume_FilesystemOnlyReboots(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	ctx := t.Context()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
	envdClient := setup.GetEnvdClient(t, ctx)

	// Boot id before pause, and a marker written to the persisted rootfs.
	bootBefore, err := utils.ExecCommandAsRootWithOutput(t, ctx, sbx, envdClient,
		"cat", "/proc/sys/kernel/random/boot_id")
	require.NoError(t, err)
	bootBefore = strings.TrimSpace(bootBefore)
	require.NotEmpty(t, bootBefore)

	const marker = "fs-only-survives-reboot"
	err = utils.ExecCommandAsRoot(t, ctx, sbx, envdClient,
		"/bin/sh", "-c", "echo "+marker+" > /home/user/fs-only-marker.txt")
	require.NoError(t, err)

	// Pause filesystem-only, then resume explicitly (allowed; cold-boots).
	pauseFilesystemOnly(t, c, sbx.SandboxID)

	// Use a generous timeout: a cold boot goes through placement, and under
	// parallel load that can take long enough that a short default timeout would
	// expire the requested end time before RebootSandbox runs (it rejects an
	// already-past end time), making the resume flaky.
	resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
		api.PostSandboxesSandboxIDResumeJSONRequestBody{Timeout: new(int32(120))}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resumeResp.StatusCode(),
		"explicit resume of a filesystem-only snapshot should succeed (cold boot)")

	// The rootfs must survive the reboot.
	got, err := utils.ExecCommandAsRootWithOutput(t, ctx, sbx, envdClient,
		"cat", "/home/user/fs-only-marker.txt")
	require.NoError(t, err)
	assert.Equal(t, marker, strings.TrimSpace(got), "rootfs marker must survive the reboot")

	// A fresh boot id proves a cold boot rather than a memory restore.
	bootAfter, err := utils.ExecCommandAsRootWithOutput(t, ctx, sbx, envdClient,
		"cat", "/proc/sys/kernel/random/boot_id")
	require.NoError(t, err)
	assert.NotEqual(t, bootBefore, strings.TrimSpace(bootAfter),
		"boot id should change — a filesystem-only resume cold-boots the guest")

	// The cold boot must restore the template's default user/workdir. A fresh
	// envd starts as root, and only the /init call re-establishes the default;
	// a memory resume hides this by restoring envd's user from RAM, so the
	// reboot path must re-send it explicitly. Run without a user header so envd
	// falls back to its default (would be root//root if /init didn't set it).
	whoami, err := utils.ExecCommandAsDefaultUserWithOutput(t, ctx, sbx, envdClient, "whoami")
	require.NoError(t, err)
	assert.Equal(t, "user", strings.TrimSpace(whoami),
		"default user after a filesystem-only reboot must be the template user, not root")

	pwd, err := utils.ExecCommandAsDefaultUserWithOutput(t, ctx, sbx, envdClient, "pwd")
	require.NoError(t, err)
	assert.Equal(t, "/home/user", strings.TrimSpace(pwd),
		"default workdir after a filesystem-only reboot must be the template user's home")
}
