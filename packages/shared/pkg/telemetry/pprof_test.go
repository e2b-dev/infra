package telemetry_test

import (
	"net/http"
	"net/http/httptest"
	_ "net/http/pprof"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func TestDefaultServeMuxBlocksPprofPaths(t *testing.T) {
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

func TestDefaultServeMuxPassesThroughNonPprofPaths(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/health",
		"/api/v1/resource",
		"/v2/token",
		"/debug/other",
	}

	for _, path := range paths {
		http.DefaultServeMux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		http.DefaultServeMux.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "non-pprof path %s should pass through", path)
	}
}

func TestDedicatedPprofMuxServes(t *testing.T) {
	t.Parallel()

	mux := telemetry.NewPprofMux()

	paths := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
	}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		mux.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "dedicated pprof mux should serve %s", path)
	}
}
