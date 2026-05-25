package userprofile

import (
	"context"
	"encoding/json"
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

func profileFromAuthUser(user supabasequeries.AuthUser) Profile {
	metadata := rawUserMetadata(user.RawUserMetaData)

	return Profile{
		UserID: user.ID,
		Email:  user.Email,
		Name:   displayNameFromMetadata(metadata),
	}
}

func displayNameFromMetadata(metadata map[string]any) string {
	firstName := FirstNonEmpty(
		metadataString(metadata, "first_name"),
		metadataString(metadata, "firstName"),
		metadataString(metadata, "given_name"),
		metadataString(metadata, "givenName"),
	)
	lastName := FirstNonEmpty(
		metadataString(metadata, "last_name"),
		metadataString(metadata, "lastName"),
		metadataString(metadata, "family_name"),
		metadataString(metadata, "familyName"),
	)
	if firstName != "" || lastName != "" {
		return strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
	}

	return FirstNonEmpty(
		metadataString(metadata, "name"),
		metadataString(metadata, "full_name"),
		metadataString(metadata, "fullName"),
	)
}

func rawUserMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}

	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil
	}

	return metadata
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}

	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(value)
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
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
