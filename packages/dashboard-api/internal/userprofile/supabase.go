package userprofile

import (
	"context"
	"strings"

	"github.com/google/uuid"

	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	supabasequeries "github.com/e2b-dev/infra/packages/db/pkg/supabase/queries"
)

type supabaseProvider struct {
	queries *supabasequeries.Queries
}

var _ Provider = (*supabaseProvider)(nil)

func NewSupabaseProvider(db *supabasedb.Client) Provider {
	return &supabaseProvider{
		queries: db.Write,
	}
}

func (p *supabaseProvider) GetProfilesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error) {
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

func (p *supabaseProvider) FindProfilesByEmail(ctx context.Context, email string) ([]Profile, error) {
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

func (p *supabaseProvider) SearchProfilesByEmail(ctx context.Context, query string, limit int32) ([]Profile, error) {
	normalizedQuery := strings.TrimSpace(query)
	if normalizedQuery == "" || limit <= 0 {
		return []Profile{}, nil
	}

	users, err := p.queries.SearchAuthUsersByEmail(ctx, supabasequeries.SearchAuthUsersByEmailParams{
		Query:       escapePostgresLikePattern(normalizedQuery),
		ResultLimit: limit,
	})
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

func escapePostgresLikePattern(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")

	return replacer.Replace(value)
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
