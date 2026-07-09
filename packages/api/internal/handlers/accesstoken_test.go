package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func TestPostAccessTokensRejectsWhenIssuanceDisabled(t *testing.T) {
	t.Parallel()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.DisableE2BAccessTokenProvisioningFlag.Key()).VariationForAll(true))
	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/access-tokens", strings.NewReader(`{"name":"my token"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	sharedauth.SetUserIDForTest(t, ginCtx, uuid.New())

	store := &APIStore{featureFlags: ff}
	store.PostAccessTokens(ginCtx)

	require.Equal(t, http.StatusGone, recorder.Code)
	require.Contains(t, recorder.Body.String(), "E2B_API_KEY")
}

// fakeAccessTokenAuthService stubs sharedauth.Service for access token
// validation; all other methods are inherited no-ops from fakeAPIKeyAuthService.
type fakeAccessTokenAuthService struct {
	fakeAPIKeyAuthService

	userID uuid.UUID
	apiErr *sharedauth.APIError
}

func (f fakeAccessTokenAuthService) ValidateAccessToken(context.Context, *gin.Context, string) (uuid.UUID, *sharedauth.APIError) {
	return f.userID, f.apiErr
}

func newAccessTokenAuthTestClient(t *testing.T, disabled bool) *featureflags.Client {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.DisableE2BAccessTokenAuthFlag.Key()).VariationForAll(disabled))
	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	return ff
}

func TestGetUserFromAccessTokenAcceptsWhenAuthEnabled(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	store := &APIStore{
		featureFlags: newAccessTokenAuthTestClient(t, false),
		authService:  fakeAccessTokenAuthService{userID: userID},
	}

	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	gotUserID, apiErr := store.GetUserFromAccessToken(t.Context(), ginCtx, "sk_e2b_valid")

	require.Nil(t, apiErr)
	require.Equal(t, userID, gotUserID)
}

func TestGetUserFromAccessTokenRejectsWhenAuthDisabled(t *testing.T) {
	t.Parallel()

	store := &APIStore{
		featureFlags: newAccessTokenAuthTestClient(t, true),
		authService:  fakeAccessTokenAuthService{userID: uuid.New()},
	}

	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	gotUserID, apiErr := store.GetUserFromAccessToken(t.Context(), ginCtx, "sk_e2b_valid")

	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusUnauthorized, apiErr.Code)
	require.Contains(t, apiErr.ClientMsg, "E2B_API_KEY")
	require.Contains(t, apiErr.ClientMsg, "https://e2b.dev/docs/migration/access-token-deprecation")
	require.Equal(t, uuid.UUID{}, gotUserID)
}

func TestGetUserFromAccessTokenInvalidTokenStillRejected(t *testing.T) {
	t.Parallel()

	validationErr := &sharedauth.APIError{
		Err:       errors.New("failed to verify access token"),
		ClientMsg: "Invalid access token format",
		Code:      http.StatusUnauthorized,
	}
	store := &APIStore{
		featureFlags: newAccessTokenAuthTestClient(t, true),
		authService:  fakeAccessTokenAuthService{apiErr: validationErr},
	}

	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	gotUserID, apiErr := store.GetUserFromAccessToken(t.Context(), ginCtx, "not-a-token")

	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusUnauthorized, apiErr.Code)
	require.Equal(t, "Invalid access token format", apiErr.ClientMsg)
	require.Equal(t, uuid.UUID{}, gotUserID)
}
