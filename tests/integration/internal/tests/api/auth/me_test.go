package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestMe(t *testing.T) {
	t.Parallel()

	t.Run("authenticated", func(t *testing.T) {
		t.Parallel()

		c := setup.GetAPIClient()
		response, err := c.GetMeWithResponse(t.Context(), setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode())
		require.NotNil(t, response.JSON200)
		require.NotEmpty(t, response.JSON200.TeamID)
		require.NotEmpty(t, response.JSON200.TeamName)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		t.Parallel()

		c := setup.GetAPIClient()
		response, err := c.GetMeWithResponse(t.Context())
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, response.StatusCode())
		require.NotNil(t, response.JSON401)
		require.Equal(t, int32(401), response.JSON401.Code)
		require.Equal(t, "no credentials found", response.JSON401.Message)
	})
}
