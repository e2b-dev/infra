package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two secrets middlewares are exercised end to end, in the order the API
// composes them, by the confidentiality tests in the handlers package - that is
// where the matched-route half of this predicate is proven. Here it is checked
// for the requests no route matches, which reach it by path alone and must be
// treated just as confidentially.
func TestIsSecretsRouteByPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "family root", path: "/secrets", want: true},
		{name: "selector", path: "/secrets/my-secret", want: true},
		{name: "unknown sub path", path: "/secrets/my-secret/versions", want: true},
		{name: "another route sharing the prefix", path: "/secretsomething", want: false},
		{name: "unrelated route", path: "/volumes", want: false},
		{name: "root", path: "/", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil)

			assert.Equal(t, test.want, IsSecretsRoute(c))
		})
	}
}

func TestNoStoreSecretsMarksEveryEngineResponse(t *testing.T) {
	t.Parallel()

	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.Use(NoStoreSecrets())
	engine.GET("/secrets", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/secrets/:secretID", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/volumes", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantNoStore bool
	}{
		{name: "family root", path: "/secrets", wantStatus: http.StatusNoContent, wantNoStore: true},
		{name: "selector", path: "/secrets/my-secret", wantStatus: http.StatusNoContent, wantNoStore: true},
		{name: "unmatched descendant 404", path: "/secrets/my-secret/versions", wantStatus: http.StatusNotFound, wantNoStore: true},
		{name: "shared prefix only", path: "/secretsfoo", wantStatus: http.StatusNotFound},
		{name: "unrelated route", path: "/volumes", wantStatus: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			require.Equal(t, test.wantStatus, recorder.Code)
			if test.wantNoStore {
				assert.Equal(t, []string{cacheControlNoStoreValue}, recorder.Header().Values(cacheControlHeader))
			} else {
				assert.Empty(t, recorder.Header().Values(cacheControlHeader))
			}
		})
	}
}
