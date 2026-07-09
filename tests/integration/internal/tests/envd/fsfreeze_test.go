package envd

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// rootfsFile is a path on the guest rootfs (the ext4 filesystem `/fsfreeze`
// quiesces). /tmp may be a separate tmpfs, so probes must target the rootfs to
// observe the freeze.
const rootfsDir = "/home/user"

// TestFsFreezeThaw exercises envd's native POST /fsfreeze and /fsthaw endpoints
// against a real guest. It is the end-to-end evidence that the primitive the
// filesystem-only pause relies on actually behaves as assumed:
//
//   - FIFREEZE quiesces the rootfs so writes block (this is *why* freezing
//     closes the sync->pause race the legacy guest sync left open: no write can
//     be acknowledged between the freeze and the VM pause),
//   - reads still succeed while frozen (it is a write barrier, not a dead envd),
//   - FITHAW restores writability (the orchestrator's pause-failure rollback),
//   - both endpoints are idempotent.
//
// Requires envd >= 0.6.6 (MinEnvdVersionForFsFreeze); the test template on this
// branch ships it.
func TestFsFreezeThaw(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	client := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, client, utils.WithAutoPause(false))
	envdClient := setup.GetEnvdClient(t, ctx)

	post := func(c context.Context, path string) int {
		t.Helper()
		var resp *http.Response
		var err error
		switch path {
		case "fsfreeze":
			resp, err = envdClient.HTTPClient.PostFsfreeze(c, setup.WithSandbox(t, sbx.SandboxID))
		case "fsthaw":
			resp, err = envdClient.HTTPClient.PostFsthaw(c, setup.WithSandbox(t, sbx.SandboxID))
		default:
			t.Fatalf("unknown path %q", path)
		}
		require.NoError(t, err)
		defer resp.Body.Close()

		return resp.StatusCode
	}

	// Safety net: always leave the rootfs thawed, even if an assertion fails
	// mid-test, so sandbox teardown can't deadlock on a frozen rootfs. Uses a
	// fresh context because t.Context() is already cancelled during cleanup.
	t.Cleanup(func() {
		thawCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if resp, err := envdClient.HTTPClient.PostFsthaw(thawCtx, setup.WithSandbox(t, sbx.SandboxID)); err == nil {
			_ = resp.Body.Close()
		}
	})

	// Baseline: a rootfs write succeeds before freezing.
	require.NoError(t, utils.ExecCommandAsRoot(t, ctx, sbx, envdClient, "touch", rootfsDir+"/before-freeze"),
		"baseline rootfs write should succeed before freeze")

	// Freeze the rootfs.
	require.Equal(t, http.StatusNoContent, post(ctx, "fsfreeze"), "freeze should return 204")

	// While frozen, a rootfs write blocks on the frozen superblock. Bound it
	// with a short deadline and require that it does NOT complete: a successful
	// (fast) write here would mean the freeze did not take.
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := utils.ExecCommandAsRoot(t, writeCtx, sbx, envdClient, "touch", rootfsDir+"/while-frozen")
	require.Error(t, err, "write to a frozen rootfs must block until the deadline")

	// Reads still work while frozen: proves it is a write barrier and that envd
	// itself is responsive (it must be, to serve the thaw below).
	out, err := utils.ExecCommandAsRootWithOutput(t, ctx, sbx, envdClient, "cat", "/etc/hostname")
	require.NoError(t, err, "reads must still work while the rootfs is frozen")
	require.NotEmpty(t, strings.TrimSpace(out))

	// Freeze is idempotent: freezing an already-frozen rootfs is a no-op success.
	require.Equal(t, http.StatusNoContent, post(ctx, "fsfreeze"), "second freeze should be an idempotent 204")

	// Thaw restores writability.
	require.Equal(t, http.StatusNoContent, post(ctx, "fsthaw"), "thaw should return 204")
	require.NoError(t, utils.ExecCommandAsRoot(t, ctx, sbx, envdClient, "touch", rootfsDir+"/after-thaw"),
		"rootfs write should succeed after thaw")

	// Thaw is idempotent: thawing an already-thawed rootfs is a no-op success.
	require.Equal(t, http.StatusNoContent, post(ctx, "fsthaw"), "second thaw should be an idempotent 204")
}
