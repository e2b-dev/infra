package userprofile

import (
	"context"
	"strings"

	"github.com/google/uuid"

	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	supabasequeries "github.com/e2b-dev/infra/packages/db/pkg/supabase/queries"
)

type Profile struct {
	UserID uuid.UUID
	Email  string
}

type Provider interface {
	GetProfiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error)
	FindUsersByEmail(ctx context.Context, email string) ([]Profile, error)
}

type SupabaseProvider struct {
	queries *supabasequeries.Queries
}

var _ Provider = (*SupabaseProvider)(nil)

func NewSupabaseProvider(db *supabasedb.Client) *SupabaseProvider {
	return &SupabaseProvider{
		queries: db.Write,
	}
}

func (p *SupabaseProvider) GetProfiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error) {
	uniqueUserIDs := uniqueUUIDs(userIDs)
	if len(uniqueUserIDs) == 0 {
		return map[uuid.UUID]Profile{}, nil
	}

	users, err := p.queries.GetAuthUsersByIDs(ctx, uniqueUserIDs)
	if err != nil {
		return nil, err
	}

	profiles := make(map[uuid.UUID]Profile, len(users))
	for _, user := range users {
		profiles[user.ID] = profileFromAuthUser(user)
	}

	return profiles, nil
}

func (p *SupabaseProvider) FindUsersByEmail(ctx context.Context, email string) ([]Profile, error) {
	normalizedEmail := strings.TrimSpace(email)
	if normalizedEmail == "" {
		return []Profile{}, nil
	}

	users, err := p.queries.GetAuthUsersByEmail(ctx, normalizedEmail)
	if err != nil {
		return nil, err
	}

	profiles := make([]Profile, 0, len(users))
	for _, user := range users {
		profiles = append(profiles, profileFromAuthUser(user))
	}

	return profiles, nil
}

func profileFromAuthUser(user supabasequeries.AuthUser) Profile {
	return Profile{
		UserID: user.ID,
		Email:  user.Email,
	}
}

func uniqueUUIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	unique := make([]uuid.UUID, 0, len(ids))

	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}

		if _, ok := seen[id]; ok {
			continue
		}

		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	return unique
}
