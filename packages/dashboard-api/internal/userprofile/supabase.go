package userprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	supabasequeries "github.com/e2b-dev/infra/packages/db/pkg/supabase/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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

func (p *supabaseProvider) GetTeamCreatorContext(ctx context.Context, userID uuid.UUID) (*sharedteamprovision.CreatorContextV1, error) {
	if userID == uuid.Nil {
		return nil, nil
	}

	authUser, err := p.queries.GetAuthUserByID(ctx, userID)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("get auth user: %w", err)
	}

	appMetadata := rawUserMetadata(authUser.RawAppMetaData)
	creatorContext := creatorContextFromMetadata(appMetadata, providerNamesFromSupabaseMetadata(appMetadata))

	if creatorContext.AuthMethod == sharedteamprovision.AuthMethodSocial && (creatorContext.IPAddress == "" || creatorContext.UserAgent == "") {
		session, sessionErr := p.queries.GetLatestAuthSessionByUserID(ctx, userID)
		if sessionErr != nil {
			if !dberrors.IsNotFoundError(sessionErr) {
				logger.L().Warn(ctx, "failed to resolve latest auth session for creator context, falling back to metadata",
					zap.String("user_id", userID.String()),
					zap.Error(sessionErr),
				)
			}
		} else {
			if creatorContext.IPAddress == "" {
				creatorContext.IPAddress = utils.DerefOrDefault(session.Ip, "")
			}
			if creatorContext.UserAgent == "" {
				creatorContext.UserAgent = utils.DerefOrDefault(session.UserAgent, "")
			}
		}
	}

	return creatorContext, nil
}

func profileFromAuthUser(user supabasequeries.AuthUser) Profile {
	userMetadata := rawUserMetadata(user.RawUserMetaData)
	appMetadata := rawUserMetadata(user.RawAppMetaData)

	return Profile{
		UserID:            user.ID,
		Email:             user.Email,
		Name:              displayNameFromMetadata(userMetadata),
		ProfilePictureURL: utils.FirstNonEmpty(metadataString(userMetadata, "picture"), metadataString(userMetadata, "avatar_url")),
		Providers:         supabaseLinkedProviders(appMetadata),
	}
}

// supabaseLinkedProviders mirrors the way Supabase records linked OAuth
// providers under raw_app_meta_data: a `providers` array plus an `provider`
// scalar for the most recently used one.
func supabaseLinkedProviders(appMetadata map[string]any) []string {
	return normalizeAuthProviders(providerNamesFromSupabaseMetadata(appMetadata))
}

func displayNameFromMetadata(metadata map[string]any) string {
	firstName := utils.FirstNonEmpty(
		metadataString(metadata, "first_name"),
		metadataString(metadata, "firstName"),
		metadataString(metadata, "given_name"),
		metadataString(metadata, "givenName"),
	)
	lastName := utils.FirstNonEmpty(
		metadataString(metadata, "last_name"),
		metadataString(metadata, "lastName"),
		metadataString(metadata, "family_name"),
		metadataString(metadata, "familyName"),
	)
	if firstName != "" || lastName != "" {
		return strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
	}

	return utils.FirstNonEmpty(
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
