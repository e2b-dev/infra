package userprofile

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

// ErrUserNotFound is returned when the requested user has no identity mapping.
var ErrUserNotFound = errors.New("user not found")

type Profile struct {
	UserID            uuid.UUID
	Email             string
	Name              string
	ProfilePictureURL string
	Providers         []string
}

type Provider interface {
	GetProfilesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error)
	FindProfilesByEmail(ctx context.Context, email string) ([]Profile, error)
	GetTeamCreatorContext(ctx context.Context, userID uuid.UUID) (*sharedteamprovision.CreatorContextV1, error)
	GetIdentityOrganizationID(ctx context.Context, subject string) (uuid.UUID, error)
	GetUserOrganizationID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	SetIdentityExternalID(ctx context.Context, subject string, externalID uuid.UUID) error
	PrepareDeleteUser(ctx context.Context, userID uuid.UUID) (DeleteUserHandle, error)
}

// DeleteUserHandle holds pre-fetched state needed to finalise user deletion
// after the database rows have been removed.
type DeleteUserHandle interface {
	// Execute removes the external identity (e.g. Ory). It must be called
	// only after the caller has already deleted the database rows.
	Execute(ctx context.Context) error
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
