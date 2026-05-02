package bearer

import (
	"errors"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

// Entry describes a single HMAC-verified bearer JWT source.
type Entry struct {
	HMAC          *HMACConfig           `json:"hmac"`
	Audiences     []string              `json:"audiences"`
	ClaimMappings jwtutil.ClaimMappings `json:"claimMappings"`
}

// HMACConfig holds HMAC secrets used for symmetric JWT verification.
type HMACConfig struct {
	Secrets []string `json:"secrets"`
}

// Normalized returns a copy with defaults applied.
func (e Entry) Normalized() Entry {
	e.ClaimMappings = e.ClaimMappings.Normalized()

	return e
}

// Validate returns an error if the entry contains invalid configuration.
func (e Entry) Validate() error {
	if e.HMAC == nil {
		return errors.New("hmac is required")
	}

	if len(e.HMAC.Secrets) == 0 {
		return errors.New("hmac.secrets must not be empty")
	}

	return nil
}
