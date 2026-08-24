package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlePreflightAnswersPreflight(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/health", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", http.MethodGet)
	r.Header.Set("Access-Control-Request-Headers", "e2b-sandbox-id,e2b-sandbox-port")
	w := httptest.NewRecorder()

	require.True(t, HandlePreflight(w, r))

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "e2b-sandbox-id,e2b-sandbox-port", w.Header().Get("Access-Control-Allow-Headers"))
	assert.NotEmpty(t, w.Header().Get("Access-Control-Max-Age"))
	assert.Empty(t, w.Header().Values("Vary"))
}

func TestHandlePreflightOmitsAllowHeadersWhenNoneRequested(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/health", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", http.MethodGet)
	w := httptest.NewRecorder()

	require.True(t, HandlePreflight(w, r))

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Header().Values("Access-Control-Allow-Headers"))
}

func TestHandlePreflightIgnoresNonPreflights(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		method  string
		headers map[string]string
	}{
		{
			name:   "get",
			method: http.MethodGet,
		},
		{
			name:    "get carrying request-method header",
			method:  http.MethodGet,
			headers: map[string]string{"Access-Control-Request-Method": http.MethodGet},
		},
		{
			name:   "bare options",
			method: http.MethodOptions,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), tt.method, "/health", nil)
			for header, value := range tt.headers {
				r.Header.Set(header, value)
			}
			w := httptest.NewRecorder()

			require.False(t, HandlePreflight(w, r))

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Empty(t, w.Header())
			assert.Empty(t, w.Body.String())
		})
	}
}

func TestErrorWritesStatusBodyAndHeader(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	Error(w, "sandbox unreachable", http.StatusBadGateway)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, "sandbox unreachable\n", w.Body.String())
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}
