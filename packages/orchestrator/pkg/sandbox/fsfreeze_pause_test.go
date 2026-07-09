//go:build linux

package sandbox

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// TestGuestPrepareFsForPause_FreezeFailureRollsBackThaw verifies the pause
// rollback contract: when POST /fsfreeze fails during a filesystem-only pause,
// the cleanup that guestPrepareFsForPause registers invokes POST /fsthaw, so a
// rootfs the kernel may have already frozen is thawed instead of leaving the
// sandbox deadlocked.
//
// This pins the orchestrator wiring. The companion integration test
// (tests/integration/.../envd/fsfreeze_test.go) proves the real FIFREEZE/FITHAW
// ioctls actually freeze and thaw a live guest; together they cover
// "freeze fails mid-pause -> thaw rolls the sandbox back".
func TestGuestPrepareFsForPause_FreezeFailureRollsBackThaw(t *testing.T) {
	t.Parallel()

	var freezeCalls, thawCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fsfreeze":
			freezeCalls.Add(1)
			// Freeze fails from the orchestrator's perspective (e.g. a timeout
			// after the kernel already began/finished freezing the rootfs).
			http.Error(w, "FIFREEZE /: simulated failure", http.StatusInternalServerError)
		case "/fsthaw":
			thawCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	s := newFsFreezeSandbox(t, srv.URL)
	cleanup := NewCleanup()

	// During the pause: freeze fails, so guestPrepareFsForPause aborts the pause.
	err := s.guestPrepareFsForPause(t.Context(), cleanup)
	require.Error(t, err, "a failed freeze must abort the filesystem-only pause")
	require.Equal(t, int32(1), freezeCalls.Load(), "freeze should have been attempted once")
	require.Zero(t, thawCalls.Load(), "thaw must not run before the pause actually aborts")

	// Pause's deferred error handler runs the cleanup chain on abort; that is
	// what fires the rollback thaw.
	require.NoError(t, cleanup.Run(t.Context()))
	require.Equal(t, int32(1), thawCalls.Load(), "an aborted pause must thaw the rootfs exactly once")
}

// TestGuestPrepareFsForPause_SuccessDoesNotThaw guards the other half of the
// contract: on a successful freeze the cleanup is NOT run (pause succeeds), so
// /fsthaw is never called — the frozen state is intended to be discarded by the
// reboot on resume, not thawed in place.
func TestGuestPrepareFsForPause_SuccessDoesNotThaw(t *testing.T) {
	t.Parallel()

	var freezeCalls, thawCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fsfreeze":
			freezeCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case "/fsthaw":
			thawCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	s := newFsFreezeSandbox(t, srv.URL)
	cleanup := NewCleanup()

	require.NoError(t, s.guestPrepareFsForPause(t.Context(), cleanup))
	require.Equal(t, int32(1), freezeCalls.Load(), "freeze should have run once")
	require.Zero(t, thawCalls.Load(), "a successful freeze must not thaw (no abort)")
}

func newFsFreezeSandbox(t *testing.T, envdURL string) *Sandbox {
	t.Helper()

	ff, err := featureflags.NewClientWithDatasource(ldtestdata.DataSource())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	token := "test-token"
	s := &Sandbox{Metadata: &Metadata{
		Config: &Config{
			RamMB: 1024,
			Envd: EnvdMetadata{
				Version:     utils.MinEnvdVersionForFsFreeze, // gate: fsfreeze supported
				AccessToken: &token,
			},
		},
		Runtime: RuntimeMetadata{SandboxID: "test-sandbox"},
	}}
	s.featureFlags = ff
	s.internalConfig.envdServerURLOverride = envdURL

	return s
}
