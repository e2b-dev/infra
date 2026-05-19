package legacy

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestVerifier_Verify(t *testing.T) {
	t.Parallel()

	const secret = "supabasejwtsecretsupabasejwtsecret"
	verifier, err := NewVerifier(Config{
		HMAC: &HMACConfig{Secrets: []string{"wrong-secret-wrong-secret", secret}},
	})
	require.NoError(t, err)

	userID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID.String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	signedToken, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	gotUserID, _, err := verifier.Verify(t.Context(), signedToken)
	require.NoError(t, err)
	require.Equal(t, userID, gotUserID)
}
