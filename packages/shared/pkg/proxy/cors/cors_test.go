package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlePreflight(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/health", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Headers", "e2b-sandbox-id,e2b-sandbox-port")
	w := httptest.NewRecorder()

	require.True(t, HandlePreflight(w, r))

	// A preflight is only honored by the browser with an ok status.
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "e2b-sandbox-id,e2b-sandbox-port", w.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, preflightMaxAge, w.Header().Get("Access-Control-Max-Age"))
	assert.Equal(t, "Origin", w.Header().Get("Vary"))
}

func TestHandlePreflightWithoutRequestedHeaders(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/health", nil)
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()

	require.True(t, HandlePreflight(w, r))

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Header().Values("Access-Control-Allow-Headers"))
}

func TestHandlePreflightIgnoresNonPreflights(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		method string
		// A bare OPTIONS without Access-Control-Request-Method is not a preflight,
		// so it has to fall through to the caller's own error handling.
		requestMethodHeader string
	}{
		{name: "GET", method: http.MethodGet},
		{name: "GET with request method header", method: http.MethodGet, requestMethodHeader: "GET"},
		{name: "bare OPTIONS", method: http.MethodOptions},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), tt.method, "/health", nil)
			if tt.requestMethodHeader != "" {
				r.Header.Set("Access-Control-Request-Method", tt.requestMethodHeader)
			}
			w := httptest.NewRecorder()

			assert.False(t, HandlePreflight(w, r))
			assert.Empty(t, w.Header(), "the response must be left untouched")
			assert.Empty(t, w.Body.String())
		})
	}
}

func TestError(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	Error(w, "Invalid sandbox port", http.StatusBadRequest)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Invalid sandbox port\n", w.Body.String())
}
