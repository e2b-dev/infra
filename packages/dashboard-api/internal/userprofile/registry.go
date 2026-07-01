package userprofile

import (
	"context"
	"maps"
	"slices"

	"github.com/google/uuid"
)

// Registry resolves the identity provider responsible for an OIDC issuer.
type Registry map[string]Provider

func (r Registry) ForIssuer(issuer string) (Provider, bool) {
	provider, ok := r[issuer]

	return provider, ok
}

// GetUserOrganizationID returns the user's Ory organization id across all
// registered providers, or uuid.Nil when none of their identities belongs to an
// organization. Issuers are checked in sorted order so the result is
// deterministic.
func (r Registry) GetUserOrganizationID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	for _, issuer := range slices.Sorted(maps.Keys(r)) {
		orgID, err := r[issuer].GetUserOrganizationID(ctx, userID)
		if err != nil {
			return uuid.Nil, err
		}
		if orgID != uuid.Nil {
			return orgID, nil
		}
	}

	return uuid.Nil, nil
}
