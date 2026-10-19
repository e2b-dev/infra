package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/identity"
)

func TestPostAdminUserProfilesResolveWithoutIdentityProvider(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/user-profiles/resolve", strings.NewReader(`{"userIds":["`+userID.String()+`"]}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{identityService: identity.NewUnavailableService()}
	store.PostAdminUserProfilesResolve(ginCtx)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{"code":503,"message":"No identity provider is configured; this endpoint is unavailable"}`, recorder.Body.String())
}
