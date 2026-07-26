package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

const testAdminToken = "test-admin-token"

// newAdminAuthRouter mirrors the admin slice of main.go's server: the generated
// routes behind the spec-driven request validator with the admin-token
// authenticator. Calling the handler directly can't catch a route that is
// registered without auth, which is the failure mode that matters for /admin/*.
func newAdminAuthRouter(t *testing.T, store *APIStore) *gin.Engine {
	t.Helper()

	swagger, err := api.GetSwagger()
	require.NoError(t, err)
	swagger.Servers = nil

	authenticationFunc := sharedauth.CreateAuthenticationFunc(
		[]sharedauth.Authenticator{
			sharedauth.NewAdminApiKeyAuthenticator(testAdminToken),
		},
		nil,
	)

	r := gin.New()
	r.Use(middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{
		// Same promotion as main.go: the validator reports auth failures as 400,
		// so the status the authenticator already wrote has to win.
		ErrorHandler: func(c *gin.Context, message string, statusCode int) {
			statusCode = max(c.Writer.Status(), statusCode)
			c.AbortWithStatusJSON(statusCode, gin.H{"code": statusCode, "message": message})
		},
		Options: openapi3filter.Options{AuthenticationFunc: authenticationFunc},
	}))
	api.RegisterHandlers(r, store)

	return r
}

func TestGetAdminTiers_RequiresAdminToken(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	router := newAdminAuthRouter(t, &APIStore{db: testDB.SqlcClient})

	for name, token := range map[string]string{
		"missing token": "",
		"wrong token":   "not-the-admin-token",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/tiers", nil)
			if token != "" {
				req.Header.Set(sharedauth.HeaderAdminToken, token)
			}

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)

			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
	}
}

func TestGetAdminTiers_ServesTiersOverHTTP(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	router := newAdminAuthRouter(t, &APIStore{db: testDB.SqlcClient})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/tiers", nil)
	req.Header.Set(sharedauth.HeaderAdminToken, testAdminToken)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp api.AdminTiersResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	assert.Contains(t, resp.Tiers, api.AdminTier{Id: "base_v1", Name: "Base tier"})
}
