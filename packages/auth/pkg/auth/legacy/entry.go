package legacy

import (
	"errors"
)

// Entry describes a single HMAC-verified legacy JWT source.
type Entry struct {
	HMAC      *HMACConfig `json:"hmac"`
	Audiences []string    `json:"audiences"`
}

// HMACConfig holds HMAC secrets used for symmetric JWT verification.
type HMACConfig struct {
	Secrets []string `json:"secrets"`
}

// Normalized returns a copy with defaults applied.
func (e Entry) Normalized() Entry {
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
