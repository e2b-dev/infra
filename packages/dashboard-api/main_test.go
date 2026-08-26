package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type legacyTeamMutationTestStore struct {
	api.ServerInterface

	reached bool
}

func (store *legacyTeamMutationTestStore) PostTeams(c *gin.Context) {
	store.reached = true
	c.Status(http.StatusNoContent)
}

func TestLegacyTeamMutationGateRunsAfterAuthentication(t *testing.T) {
	t.Parallel()

	dataSource := ldtestdata.DataSource()
	featureFlags, err := featureflags.NewClientWithDatasource(dataSource)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, featureFlags.Close(context.WithoutCancel(t.Context())))
	})
	swagger, err := api.GetSpec()
	require.NoError(t, err)
	swagger.Servers = nil

	store := &legacyTeamMutationTestStore{}
	authenticationFunc := func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
		if input.RequestValidationInput.Request.Header.Get(sharedauth.HeaderAuthorization) == "" {
			return errors.New("authentication required")
		}

		return nil
	}
	server := newHTTPServer(
		0,
		logger.NewNopLogger(),
		telemetry.NewNoopClient(),
		swagger,
		authenticationFunc,
		featureFlags,
		store,
	)

	serve := func(authorization string) *httptest.ResponseRecorder {
		t.Helper()

		store.reached = false
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/teams", strings.NewReader(`{"name":"team"}`))
		request.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			request.Header.Set(sharedauth.HeaderAuthorization, authorization)
		}
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)

		return response
	}

	response := serve("")
	require.NotEqual(t, http.StatusPreconditionFailed, response.Code)
	assert.False(t, store.reached)
	unauthenticatedStatus := response.Code

	dataSource.Update(dataSource.Flag(featureflags.DisableLegacyTeamMutationsFlag.Key()).VariationForAll(true))

	response = serve("")
	assert.Equal(t, unauthenticatedStatus, response.Code)
	assert.False(t, store.reached)

	response = serve("Bearer test-token")
	assert.Equal(t, http.StatusPreconditionFailed, response.Code)
	assert.JSONEq(t, `{"code":412,"message":"Legacy team mutations are no longer available. Use the workspace API."}`, response.Body.String())
	assert.False(t, store.reached)
}
