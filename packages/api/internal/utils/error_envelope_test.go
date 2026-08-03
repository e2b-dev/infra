package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apispec "github.com/e2b-dev/infra/packages/api/internal/api"
	custommw "github.com/e2b-dev/infra/packages/api/internal/middleware"
)

const testBodyLimit = 1 << 10

// envelopeRouter mirrors main.go's middleware chain: the body size limiter,
// then the OpenAPI validator wired to ErrorHandler.
func envelopeRouter(t *testing.T, authOK bool) *gin.Engine {
	t.Helper()

	swagger, err := apispec.GetSpec()
	require.NoError(t, err)
	swagger.Servers = nil

	authFn := func(context.Context, *openapi3filter.AuthenticationInput) error {
		if authOK {
			return nil
		}

		return http.ErrNoCookie
	}

	r := gin.New()
	r.Use(
		custommw.RequestSizeLimiter(testBodyLimit),
		middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{
			ErrorHandler: func(c *gin.Context, message string, fallbackStatusCode int) {
				ErrorHandler(c, message, max(c.Writer.Status(), fallbackStatusCode))
			},
			MultiErrorHandler: MultiErrorHandler,
			Options: openapi3filter.Options{
				AuthenticationFunc: authFn,
				MultiError:         true,
			},
			SilenceServersWarning: true,
		}),
	)
	r.POST("/templates", func(c *gin.Context) { c.JSON(http.StatusAccepted, gin.H{"ok": true}) })

	return r
}

// TestErrorResponsesUseTheDocumentedEnvelope covers EN-919: every rejection
// path must answer with the Error schema the spec promises.
func TestErrorResponsesUseTheDocumentedEnvelope(t *testing.T) {
	t.Parallel()

	oversized := fmt.Sprintf(`{"dockerfile":%q,"memoryMB":512,"cpuCount":2}`, strings.Repeat("A", testBodyLimit*4))

	tests := []struct {
		name        string
		authOK      bool
		method      string
		path        string
		body        string
		contentType string
		wantStatus  int
	}{
		{
			name: "body over the size limit", authOK: true,
			method: http.MethodPost, path: "/templates", body: oversized,
			contentType: "application/json", wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "unauthenticated", authOK: false,
			method: http.MethodPost, path: "/templates", body: `{"dockerfile":"FROM alpine"}`,
			contentType: "application/json", wantStatus: http.StatusBadRequest,
		},
		{
			name: "malformed json", authOK: true,
			method: http.MethodPost, path: "/templates", body: `{not json`,
			contentType: "application/json", wantStatus: http.StatusBadRequest,
		},
		{
			name: "body does not match the schema", authOK: true,
			method: http.MethodPost, path: "/templates", body: `{}`,
			contentType: "application/json", wantStatus: http.StatusBadRequest,
		},
		{
			name: "deprecated multipart upload", authOK: true,
			method: http.MethodPost, path: "/templates", body: `dockerfile=x`,
			contentType: "multipart/form-data; boundary=x", wantStatus: http.StatusBadRequest,
		},
		{
			name: "unknown route", authOK: true,
			method: http.MethodPost, path: "/no-such-endpoint", body: `{}`,
			contentType: "application/json", wantStatus: http.StatusNotFound,
		},
		{
			name: "method not allowed", authOK: true,
			method: http.MethodPatch, path: "/templates", body: `{}`,
			contentType: "application/json", wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			req.Header.Set("Authorization", "Bearer token")

			rr := httptest.NewRecorder()
			envelopeRouter(t, tt.authOK).ServeHTTP(rr, req)

			assert.Equal(t, tt.wantStatus, rr.Code)

			var envelope apispec.Error
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &envelope),
				"error response is not the documented Error schema: %q", rr.Body.String())

			assert.NotZero(t, envelope.Code, "error response has no code: %q", rr.Body.String())
			assert.NotEmpty(t, envelope.Message, "error response has no message: %q", rr.Body.String())
		})
	}
}

// TestErrorHandlerLeavesACommittedResponseAlone is the EN-919 backstop:
// ErrorHandler must not append the envelope to an already-committed response.
func TestErrorHandlerLeavesACommittedResponseAlone(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/templates", strings.NewReader("{}"))

	c.String(http.StatusRequestEntityTooLarge, "request too large")

	ErrorHandler(c, "reading failed: request too large", http.StatusRequestEntityTooLarge)

	assert.True(t, c.IsAborted(), "the request should still be aborted")
	assert.Equal(t, "request too large", rr.Body.String(), "the committed body must be left untouched")
}
