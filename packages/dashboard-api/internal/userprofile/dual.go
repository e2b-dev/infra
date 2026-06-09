package userprofile

import (
	"context"
	"maps"

	"github.com/google/uuid"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
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

	secondary, err := p.secondary.GetProfilesByUserID(ctx, userIDs)
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

	if len(secondary) > 0 {
		return uniqueProfilesByUserID(secondary), nil
	}

	return uniqueProfilesByUserID(primary), nil
}

func (p *dualProvider) GetTeamCreatorContext(ctx context.Context, userID uuid.UUID) (*sharedteamprovision.CreatorContextV1, error) {
	secondary, err := p.secondary.GetTeamCreatorContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	if secondary != nil {
		return secondary, nil
	}

	primary, err := p.primary.GetTeamCreatorContext(ctx, userID)
	if err != nil {
		return nil, err
	}

	return primary, nil
}

func uniqueProfilesByUserID(profiles []Profile) []Profile {
	seen := make(map[uuid.UUID]struct{}, len(profiles))
	unique := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		if _, ok := seen[profile.UserID]; ok {
			continue
		}
		seen[profile.UserID] = struct{}{}
		unique = append(unique, profile)
	}

	return unique
}
