package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AuthProviderIdentity is the normalized identity extracted from a validated auth provider JWT.
type AuthProviderIdentity struct {
	UserID uuid.UUID
	Email  string
	Claims jwt.MapClaims
}

func identityFromClaims(claims jwt.MapClaims, userIDClaim, emailClaim string) *AuthProviderIdentity {
	identity := &AuthProviderIdentity{Claims: claims}
	if claimValue, ok := claimString(claims, userIDClaim); ok {
		userID, err := uuid.Parse(claimValue)
		if err == nil {
			identity.UserID = userID
		}
	}
	if email, ok := claimString(claims, emailClaim); ok {
		identity.Email = email
	}

	return identity
}

func claimString(claims jwt.MapClaims, name string) (string, bool) {
	value, ok := claims[name]
	if !ok {
		return "", false
	}

	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	case []string:
		if len(typed) == 0 {
			return "", false
		}

		return typed[0], typed[0] != ""
	case []any:
		if len(typed) == 0 {
			return "", false
		}
		first, ok := typed[0].(string)

		return first, ok && first != ""
	default:
		return "", false
	}
}
