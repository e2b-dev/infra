package template

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const chromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"

// errorHandler erases the concrete type parameter so every constructor can go
// into one table.
type errorHandler func(w http.ResponseWriter, r *http.Request) error

func handlers(t *testing.T) map[string]errorHandler {
	t.Helper()

	const (
		sandboxID = "im9r2ycjiy2534qsdy1oo"
		host      = "49983-im9r2ycjiy2534qsdy1oo.e2b.app"
	)

	return map[string]errorHandler{
		"sandbox_not_found":                NewSandboxNotFoundError(sandboxID, host).HandleError,
		"sandbox_still_transitioning":      NewSandboxStillTransitioningError(sandboxID, host).HandleError,
		"sandbox_resume_permission_denied": NewSandboxResumePermissionDeniedError(sandboxID, host).HandleError,
		"port_closed":                      NewPortClosedError(sandboxID, host, 3000).HandleError,
		"team_sandbox_limit":               NewTeamSandboxLimitError(sandboxID, host, "limit reached").HandleError,
		"sandbox_too_many_connections":     NewSandboxTooManyConnectionsError(sandboxID, host, 1024).HandleError,
		"traffic_access_token_missing":     NewTrafficAccessTokenMissingHeader(sandboxID, host, "X-Access-Token").HandleError,
		"traffic_access_token_invalid":     NewTrafficAccessTokenInvalidHeader(sandboxID, host, "X-Access-Token").HandleError,
	}
}

// Every template is a response the proxy synthesizes in place of a sandbox that
// would have sent its own CORS headers, so all of them need ours. Driving the
// whole set keeps a newly added template from silently missing the header.
func TestHandleErrorSetsCORSHeaders(t *testing.T) {
	t.Parallel()

	for name, handle := range handlers(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, mode := range []struct {
				name      string
				userAgent string
			}{
				{name: "json", userAgent: ""},
				{name: "html", userAgent: chromeUserAgent},
			} {
				t.Run(mode.name, func(t *testing.T) {
					t.Parallel()

					r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
					r.Header.Set("Origin", "https://app.example.com")
					r.Header.Set("User-Agent", mode.userAgent)
					w := httptest.NewRecorder()

					require.NoError(t, handle(w, r))
					assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
					assert.Equal(t, "Origin", w.Header().Get("Vary"))
					assert.NotEmpty(t, w.Body.String())
				})
			}
		})
	}
}

// Browser JS cannot override the User-Agent, so User-Agent sniffing alone sends
// an HTML page to callers that are about to parse it as JSON.
func TestHandleErrorContentNegotiation(t *testing.T) {
	t.Parallel()

	const (
		htmlContentType = "text/html; charset=utf-8"
		jsonContentType = "application/json; charset=utf-8"
	)

	for _, tt := range []struct {
		name        string
		headers     map[string]string
		contentType string
	}{
		{
			name:        "no user agent",
			contentType: jsonContentType,
		},
		{
			name:        "top-level navigation",
			headers:     map[string]string{"User-Agent": chromeUserAgent, "Sec-Fetch-Mode": "navigate", "Accept": "text/html,application/xhtml+xml,*/*"},
			contentType: htmlContentType,
		},
		{
			name:        "bare browser user agent",
			headers:     map[string]string{"User-Agent": chromeUserAgent},
			contentType: htmlContentType,
		},
		{
			name:        "browser user agent accepting html",
			headers:     map[string]string{"User-Agent": chromeUserAgent, "Accept": "text/html"},
			contentType: htmlContentType,
		},
		{
			name:        "browser user agent accepting json",
			headers:     map[string]string{"User-Agent": chromeUserAgent, "Accept": "application/json"},
			contentType: jsonContentType,
		},
		{
			// What a default fetch() from page scripts looks like: browser
			// User-Agent, no JSON preference in Accept.
			name:        "cross-origin fetch",
			headers:     map[string]string{"User-Agent": chromeUserAgent, "Sec-Fetch-Mode": "cors", "Accept": "*/*"},
			contentType: jsonContentType,
		},
		{
			name:        "same-origin fetch",
			headers:     map[string]string{"User-Agent": chromeUserAgent, "Sec-Fetch-Mode": "same-origin", "Accept": "*/*"},
			contentType: jsonContentType,
		},
		{
			name:        "legacy xhr",
			headers:     map[string]string{"User-Agent": chromeUserAgent, "X-Requested-With": "XMLHttpRequest"},
			contentType: jsonContentType,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
			for header, value := range tt.headers {
				r.Header.Set(header, value)
			}
			w := httptest.NewRecorder()

			require.NoError(t, NewSandboxNotFoundError("im9r2ycjiy2534qsdy1oo", "e2b.app").HandleError(w, r))

			assert.Equal(t, http.StatusBadGateway, w.Code)
			assert.Equal(t, tt.contentType, w.Header().Get("Content-Type"))
		})
	}
}

func TestHandleErrorRejectsInvalidStatusCode(t *testing.T) {
	t.Parallel()

	e := &TemplatedError[sandboxNotFoundData]{
		template: sandboxNotFoundHtmlTemplate,
		vars:     sandboxNotFoundData{Code: 0},
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	require.Error(t, e.HandleError(w, r))
	assert.Empty(t, w.Body.String())
}
