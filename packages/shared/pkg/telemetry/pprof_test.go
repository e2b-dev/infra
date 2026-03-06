package telemetry_test

import (
	"net/http"
	"net/http/httptest"
	_ "net/http/pprof"
	"testing"

	"github.com/stretchr/testify/assert"
)

// This should test the removal inside the same package, which should be more struct than the external package test.
func TestDefaultServeMuxHasNoPprof(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)

	http.DefaultServeMux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
