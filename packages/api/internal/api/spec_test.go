package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

// TestSpecSecuritySchemeHeaderNames asserts that the OpenAPI spec's security
// scheme header names stay in sync with the constants defined in the shared
// auth package. A drift between the two leads to silent authentication
// failures.
func TestSpecSecuritySchemeHeaderNames(t *testing.T) {
	t.Parallel()

	swagger, err := GetSpec()
	require.NoError(t, err)
	require.NotNil(t, swagger.Components)

	cases := []struct {
		schemeName     string
		expectedHeader string
	}{
		{"ApiKeyAuth", auth.HeaderAPIKey},
		{"Supabase1TokenAuth", auth.HeaderSupabaseToken},
		{"Supabase2TeamAuth", auth.HeaderSupabaseTeam},
		{"AuthProviderTeamAuth", auth.HeaderTeamID},
		{"AdminApiKeyAuth", auth.HeaderAdminToken},
		{"AdminTeamAuth", auth.HeaderTeamID},
	}

	for _, tc := range cases {
		t.Run(tc.schemeName, func(t *testing.T) {
			t.Parallel()

			ref, ok := swagger.Components.SecuritySchemes[tc.schemeName]
			require.True(t, ok, "security scheme %q not found in spec", tc.schemeName)
			require.NotNil(t, ref.Value, "security scheme %q has nil value", tc.schemeName)
			require.Equal(t, tc.expectedHeader, ref.Value.Name,
				"security scheme %q header name in spec does not match auth constant", tc.schemeName)
		})
	}
}

// TestAuthProviderTeamAuthHeaderRoutes verifies that a request carrying the
// X-Team-ID header reaches the AuthenticationFunc via the openapi3filter, and
// that the header value is the one extracted by the auth middleware.
func TestAuthProviderTeamAuthHeaderRoutes(t *testing.T) {
	t.Parallel()

	swagger, err := GetSpec()
	require.NoError(t, err)
	// Clear servers to avoid spurious validation warnings/errors with httptest hosts.
	swagger.Servers = nil

	const wantToken = "team-token-value"

	var (
		gotSchemeName string
		gotToken      string
	)

	// Map each security scheme to the header that satisfies it. We require
	// the header for every scheme to model the production setup, where
	// missing headers cause auth failure.
	schemeHeaders := map[string]string{
		"ApiKeyAuth":             auth.HeaderAPIKey,
		"AccessTokenAuth":        auth.HeaderAuthorization,
		"Supabase1TokenAuth":     auth.HeaderSupabaseToken,
		"Supabase2TeamAuth":      auth.HeaderSupabaseTeam,
		"AuthProviderBearerAuth": auth.HeaderAuthorization,
		"AuthProviderTeamAuth":   auth.HeaderTeamID,
		"AdminApiKeyAuth":        auth.HeaderAdminToken,
		"AdminTeamAuth":          auth.HeaderTeamID,
	}

	authFn := func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
		header, ok := schemeHeaders[input.SecuritySchemeName]
		if !ok {
			return http.ErrNoCookie
		}

		value := input.RequestValidationInput.Request.Header.Get(header)
		if value == "" {
			return http.ErrNoCookie
		}

		if input.SecuritySchemeName == "AuthProviderTeamAuth" {
			gotSchemeName = input.SecuritySchemeName
			gotToken = value
		}

		return nil
	}

	r := gin.New()
	r.Use(middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: authFn,
			MultiError:         true,
		},
		SilenceServersWarning: true,
	}))

	// Register a catch-all handler so any path with the right method succeeds
	// once auth passes. The OAPI middleware will reject unknown routes before
	// the handler runs, but valid spec routes will get here.
	r.NoRoute(func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.Any("/*any", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Pick a route protected by AuthProviderTeamAuth. /api-keys requires it.
	makeReq := func(headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api-keys", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		return rr
	}

	// Without any auth header, no scheme passes -> middleware rejects.
	rr := makeReq(nil)
	require.NotEqual(t, http.StatusOK, rr.Code, "request without auth header should be rejected")

	// With both the AuthProviderBearerAuth Authorization header and the
	// AuthProviderTeamAuth X-Team-ID header, the second security requirement
	// in the spec is satisfied.
	rr = makeReq(map[string]string{
		auth.HeaderAuthorization: "Bearer some-bearer-token",
		auth.HeaderTeamID:        wantToken,
	})
	require.Equal(t, http.StatusOK, rr.Code, "request with %s header should pass auth (body: %s)", auth.HeaderTeamID, rr.Body.String())
	require.Equal(t, "AuthProviderTeamAuth", gotSchemeName)
	require.Equal(t, wantToken, gotToken)
}

// TestAdminTeamAuthSchemeOrder verifies that the admin security
// schemes validate the admin token before the team header. kin-openapi sorts
// schemes by name inside one security requirement.
func TestAdminTeamAuthSchemeOrder(t *testing.T) {
	t.Parallel()

	swagger, err := GetSpec()
	require.NoError(t, err)
	swagger.Servers = nil

	var adminSchemeOrder []string

	schemeHeaders := map[string]string{
		"ApiKeyAuth":             auth.HeaderAPIKey,
		"AccessTokenAuth":        auth.HeaderAuthorization,
		"Supabase1TokenAuth":     auth.HeaderSupabaseToken,
		"Supabase2TeamAuth":      auth.HeaderSupabaseTeam,
		"AuthProviderBearerAuth": auth.HeaderAuthorization,
		"AuthProviderTeamAuth":   auth.HeaderTeamID,
		"AdminApiKeyAuth":        auth.HeaderAdminToken,
		"AdminTeamAuth":          auth.HeaderTeamID,
	}

	authFn := func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
		header, ok := schemeHeaders[input.SecuritySchemeName]
		if !ok {
			return http.ErrNoCookie
		}

		if value := input.RequestValidationInput.Request.Header.Get(header); value == "" {
			return http.ErrNoCookie
		}

		switch input.SecuritySchemeName {
		case "AdminApiKeyAuth", "AdminTeamAuth":
			adminSchemeOrder = append(adminSchemeOrder, input.SecuritySchemeName)
		}

		return nil
	}

	r := gin.New()
	r.Use(middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: authFn,
			MultiError:         true,
		},
		SilenceServersWarning: true,
	}))

	r.NoRoute(func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.Any("/*any", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api-keys", nil)
	req.Header.Set(auth.HeaderAdminToken, "admin-token")
	req.Header.Set(auth.HeaderTeamID, "team-id")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "request with admin token and team header should pass auth (body: %s)", rr.Body.String())
	require.Equal(t, []string{"AdminApiKeyAuth", "AdminTeamAuth"}, adminSchemeOrder)
}
