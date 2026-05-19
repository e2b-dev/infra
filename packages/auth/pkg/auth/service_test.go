package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestAuthService_ValidateAuthProviderTokenNilVerifier ensures that when
// AUTH_PROVIDER_CONFIG is unset (verifier is nil), ValidateAuthProviderToken
// denies the request with 401 instead of panicking on a nil pointer.
func TestAuthService_ValidateAuthProviderTokenNilVerifier(t *testing.T) {
	t.Parallel()

	svc := NewAuthService[*testTeam](nil, nil, nil)

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	userID, apiErr := svc.ValidateAuthProviderToken(t.Context(), ginCtx, "any-token")

	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusUnauthorized, apiErr.Code)
	require.Equal(t, "Backend authentication failed", apiErr.ClientMsg)
	require.Equal(t, [16]byte{}, [16]byte(userID))
}

// testTeam is a minimal TeamItem implementation for tests in this file.
type testTeam struct{ id string }

func (t *testTeam) TeamID() string { return t.id }
