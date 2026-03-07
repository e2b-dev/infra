package telemetry_test

import (
	"net/http"
	"net/http/httptest"
	_ "net/http/pprof"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func TestNewPprofMuxServesPprof(t *testing.T) {
	t.Parallel()

	mux := telemetry.NewPprofMux()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDefaultServeMuxBlocksDebugPaths(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/debug/pprof",
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
		"/debug/pprof/trace",
		"/debug/pprof/heap",
	}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		http.DefaultServeMux.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, "expected 404 for %s", path)
	}
}

func TestDefaultServeMuxPassesThroughNonDebugPaths(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/",
		"/health",
		"/api/v1/resource",
		"/v2/token",
		"/debugger",
		"/debug/other",
	}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		http.DefaultServeMux.ServeHTTP(rec, req)

		assert.NotEqual(t, http.StatusForbidden, rec.Code, "non-debug path %s should not be blocked", path)
	}
}
