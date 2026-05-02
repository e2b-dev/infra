package jwtutil

import (
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// AudienceMatchPolicy controls how a token's `aud` claim is checked against
// the configured `audiences` list.
//
// The field exists for forward compatibility with future policies. Today
// the only accepted value is `MatchAny` (or empty, which is treated as
// `MatchAny`).
type AudienceMatchPolicy string

const (
	// AudienceMatchAny passes verification when at least one configured
	// audience is present in the token's `aud` claim.
	AudienceMatchAny AudienceMatchPolicy = "MatchAny"
)

// ValidateAudienceMatchPolicy ensures the policy is allowed for the given
// audiences. It mirrors Kubernetes' apiserver validation:
//
//   - audiences must be non-empty.
//   - With more than one audience, the policy must be MatchAny.
//   - With a single audience, the policy must be empty or MatchAny.
func ValidateAudienceMatchPolicy(policy AudienceMatchPolicy, audiences []string) error {
	if len(audiences) == 0 {
		return errors.New("audiences must contain at least one entry")
	}

	if len(audiences) > 1 && policy != AudienceMatchAny {
		return fmt.Errorf("audienceMatchPolicy must be %q for multiple audiences", AudienceMatchAny)
	}

	if len(audiences) == 1 && policy != "" && policy != AudienceMatchAny {
		return fmt.Errorf("audienceMatchPolicy must be empty or %q for a single audience", AudienceMatchAny)
	}

	return nil
}

// ValidateAudience checks the token's `aud` claim against the configured
// audiences. Verification passes when at least one configured audience is
// present in the token's `aud` claim.
//
// An empty `audiences` slice is treated as "no audience restriction" and
// always passes.
func ValidateAudience(claims jwt.MapClaims, audiences []string) error {
	if len(audiences) == 0 {
		return nil
	}

	tokenAudiences, err := ExtractAudiences(claims)
	if err != nil {
		return err
	}

	tokenSet := make(map[string]struct{}, len(tokenAudiences))
	for _, aud := range tokenAudiences {
		tokenSet[aud] = struct{}{}
	}

	for _, want := range audiences {
		if _, ok := tokenSet[want]; ok {
			return nil
		}
	}

	return errors.New("token audience does not match any configured audience")
}

// ExtractAudiences returns the list of audiences from the token's `aud` claim.
// It accepts the standard string, []string, and []any encodings.
func ExtractAudiences(claims jwt.MapClaims) ([]string, error) {
	value, ok := claims["aud"]
	if !ok {
		return nil, errors.New("token is missing aud claim")
	}

	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case []string:
		return typed, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("token aud claim contains non-string value %v", item)
			}

			out = append(out, s)
		}

		return out, nil
	default:
		return nil, fmt.Errorf("token aud claim has unsupported type %T", value)
	}
}
