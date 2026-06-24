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
