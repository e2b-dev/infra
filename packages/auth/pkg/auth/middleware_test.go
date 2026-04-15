package auth

import (
	"testing"

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
