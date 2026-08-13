package template

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const chromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.3"

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
