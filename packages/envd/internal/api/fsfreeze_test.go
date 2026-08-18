package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
)

type fakeFreezer struct {
	mu        sync.Mutex
	frozen    []string
	thawed    []string
	freezeErr error
	thawErr   error
}

func (f *fakeFreezer) Freeze(mountpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.freezeErr != nil {
		return f.freezeErr
	}
	f.frozen = append(f.frozen, mountpoint)

	return nil
}

func (f *fakeFreezer) Thaw(mountpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.thawErr != nil {
		return f.thawErr
	}
	f.thawed = append(f.thawed, mountpoint)

	return nil
}

func newAPIWithFreezer(f *fakeFreezer) *API {
	api := newAPIWithCgroupManager(cgroups.NewNoopManager())
	api.fsFreezer = f

	return api
}

// These tests cover the handlers against a fake freezer: the status codes, the
// mountpoint, and the idempotency the orchestrator relies on. They say nothing
// about FIFREEZE itself.
//
// The ioctls are covered by TestFreezeBlocksWritesAndThawReleasesThem in
// internal/services/fsfreeze, which freezes a real ext4 mount and shows that
// writes then block while reads do not. That assertion used to live in an
// integration test calling POST /fsfreeze on a live guest; it moved here when the
// sandbox proxy stopped routing control-plane routes, which left the integration
// suite no way to reach envd.
//
// What no test covers any more is the two joined up on a real guest: this
// handler, the real freezer and an actual sandbox rootfs. The filesystem-only
// pause tests do not close that — they assert a marker survives pause and resume,
// which also holds when the freeze is a no-op, because the pause falls back to an
// exec fsfreeze or a plain guest sync (see guestPrepareFsForPause in the
// orchestrator).

func TestPostFsfreeze(t *testing.T) {
	t.Parallel()

	t.Run("freezes the rootfs", func(t *testing.T) {
		t.Parallel()
		f := &fakeFreezer{}
		api := newAPIWithFreezer(f)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/fsfreeze", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostFsfreeze(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, []string{rootfsMountpoint}, f.frozen)
	})

	t.Run("returns 500 on freeze error", func(t *testing.T) {
		t.Parallel()
		f := &fakeFreezer{freezeErr: errors.New("FIFREEZE /: io error")}
		api := newAPIWithFreezer(f)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/fsfreeze", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostFsfreeze(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Empty(t, f.frozen)
	})
}

func TestPostFsthaw(t *testing.T) {
	t.Parallel()

	t.Run("thaws the rootfs", func(t *testing.T) {
		t.Parallel()
		f := &fakeFreezer{}
		api := newAPIWithFreezer(f)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/fsthaw", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostFsthaw(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, []string{rootfsMountpoint}, f.thawed)
	})

	t.Run("returns 500 on thaw error", func(t *testing.T) {
		t.Parallel()
		f := &fakeFreezer{thawErr: errors.New("FITHAW /: io error")}
		api := newAPIWithFreezer(f)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/fsthaw", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostFsthaw(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Empty(t, f.thawed)
	})
}
