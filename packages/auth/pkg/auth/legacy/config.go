package legacy

import (
	"errors"
)

// Config describes a single HMAC-verified legacy JWT source.
type Config struct {
	HMAC *HMACConfig `json:"hmac"`
}

// HMACConfig holds HMAC secrets used for symmetric JWT verification.
type HMACConfig struct {
	Secrets []string `json:"secrets"`
}

// Normalized returns a copy with defaults applied.
func (e Config) Normalized() Config {
	return e
}

// Validate returns an error if the config contains invalid configuration.
func (e Config) Validate() error {
	if e.HMAC == nil {
		return errors.New("hmac is required")
	}

	if len(e.HMAC.Secrets) == 0 {
		return errors.New("hmac.secrets must not be empty")
	}

	return nil
}
