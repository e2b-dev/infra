package jwtutil

import "strings"

// DefaultUserIDClaim is the JWT claim name used for the user identifier when
// no explicit mapping is configured.
const DefaultUserIDClaim = "sub"

// ClaimMappings declares which JWT claim is mapped to which internal identity
// field.
type ClaimMappings struct {
	Username ClaimMapping `json:"username"`
}

// ClaimMapping declares the source claim for a single mapping.
type ClaimMapping struct {
	Claim string `json:"claim"`
}

// Normalized returns a copy with default values applied.
func (m ClaimMappings) Normalized() ClaimMappings {
	m.Username.Claim = strings.TrimSpace(m.Username.Claim)
	if m.Username.Claim == "" {
		m.Username.Claim = DefaultUserIDClaim
	}

	return m
}
