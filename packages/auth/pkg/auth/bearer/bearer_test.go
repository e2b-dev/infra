package bearer

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

func TestVerifier_Verify(t *testing.T) {
	t.Parallel()

	const secret = "supabasejwtsecretsupabasejwtsecret"
	verifier, err := NewVerifier(Entry{
		HMAC: &HMACConfig{Secrets: []string{"wrong-secret-wrong-secret", secret}},
		ClaimMappings: jwtutil.ClaimMappings{
			Username: jwtutil.ClaimMapping{Claim: "sub"},
		},
	})
	require.NoError(t, err)

	userID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID.String(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	signedToken, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	identity, err := verifier.Verify(t.Context(), signedToken)
	require.NoError(t, err)
	require.Equal(t, userID, identity.UserID)
}

func TestVerifier_AudienceMatch(t *testing.T) {
	t.Parallel()

	const secret = "supabasejwtsecretsupabasejwtsecret"
	verifier, err := NewVerifier(Entry{
		HMAC:      &HMACConfig{Secrets: []string{secret}},
		Audiences: []string{"audience-a", "audience-b"},
		ClaimMappings: jwtutil.ClaimMappings{
			Username: jwtutil.ClaimMapping{Claim: "sub"},
		},
	})
	require.NoError(t, err)

	userID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID.String(),
		"aud": "audience-b",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	signedToken, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	identity, err := verifier.Verify(t.Context(), signedToken)
	require.NoError(t, err)
	require.Equal(t, userID, identity.UserID)
}

func TestVerifier_AudienceMatchRejectsMismatch(t *testing.T) {
	t.Parallel()

	const secret = "supabasejwtsecretsupabasejwtsecret"
	verifier, err := NewVerifier(Entry{
		HMAC:      &HMACConfig{Secrets: []string{secret}},
		Audiences: []string{"audience-a"},
		ClaimMappings: jwtutil.ClaimMappings{
			Username: jwtutil.ClaimMapping{Claim: "sub"},
		},
	})
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.NewString(),
		"aud": "other-audience",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	signedToken, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	_, err = verifier.Verify(t.Context(), signedToken)
	require.Error(t, err)
}
