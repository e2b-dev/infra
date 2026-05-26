package userprofile

import (
	"context"
	"maps"

	"github.com/google/uuid"
)

type dualProvider struct {
	primary   Provider
	secondary Provider
}

var _ Provider = (*dualProvider)(nil)

func newDualProvider(primary, secondary Provider) *dualProvider {
	return &dualProvider{primary: primary, secondary: secondary}
}

func (p *dualProvider) GetProfilesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error) {
	primary, err := p.primary.GetProfilesByUserID(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	missing := make([]uuid.UUID, 0, len(userIDs))
	for _, id := range userIDs {
		if _, ok := primary[id]; ok {
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return primary, nil
	}

	secondary, err := p.secondary.GetProfilesByUserID(ctx, missing)
	if err != nil {
		return nil, err
	}

	maps.Copy(primary, secondary)

	return primary, nil
}

func (p *dualProvider) FindProfilesByEmail(ctx context.Context, email string) ([]Profile, error) {
	primary, err := p.primary.FindProfilesByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	secondary, err := p.secondary.FindProfilesByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	seen := make(map[uuid.UUID]struct{}, len(primary)+len(secondary))
	merged := make([]Profile, 0, len(primary)+len(secondary))
	for _, source := range [][]Profile{primary, secondary} {
		for _, profile := range source {
			if _, ok := seen[profile.UserID]; ok {
				continue
			}
			seen[profile.UserID] = struct{}{}
			merged = append(merged, profile)
		}
	}

	return merged, nil
}
