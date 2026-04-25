package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAdminValidationFunction(t *testing.T) {
	t.Parallel()

	validate := adminValidationFunction("super-secret-token")

	t.Run("accepts matching token", func(t *testing.T) {
		t.Parallel()

		_, err := validate(t.Context(), nil, "super-secret-token")
		require.Nil(t, err)
	})

	t.Run("rejects non-matching token", func(t *testing.T) {
		t.Parallel()

		_, err := validate(t.Context(), nil, "super-secret-tokem")
		require.NotNil(t, err)
		require.Equal(t, 401, err.Code)
	})
}

func TestAuthProviderTokenAuthenticator(t *testing.T) {
	t.Parallel()

	authenticator := NewAuthProviderTokenAuthenticator(nil)
	common, ok := authenticator.(*CommonAuthenticator[uuid.UUID])
	require.True(t, ok)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderAuthorization, "Bearer ey.token")

	token, err := common.GetHeaderKeysFromRequest(req)
	require.NoError(t, err)
	require.Equal(t, "ey.token", token)
}
