package jwtutil

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Identity is the normalized identity extracted from a validated JWT.
type Identity struct {
	UserID uuid.UUID
	Claims jwt.MapClaims
}

// IdentityFromClaims builds an Identity from the supplied claims. The
// userIDClaim names the claim whose value should be parsed as a UUID and used
// as the user identifier. If the claim is missing or unparseable, the
// returned Identity has a zero UserID but still contains the raw claims.
func IdentityFromClaims(claims jwt.MapClaims, userIDClaim string) *Identity {
	identity := &Identity{Claims: claims}
	if claimValue, ok := claimString(claims, userIDClaim); ok {
		userID, err := uuid.Parse(claimValue)
		if err == nil {
			identity.UserID = userID
		}
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
