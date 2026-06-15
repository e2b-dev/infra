package userprofile

import (
	"context"
	"errors"
	"fmt"

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
	// PrepareDeleteUser resolves the external identity references for the
	// given user so they can be removed after the database rows are gone.
	PrepareDeleteUser(ctx context.Context, userID uuid.UUID) (DeleteUserHandle, error)
}

// DeleteUserHandle holds pre-fetched state needed to finalise user deletion
// after the database rows have been removed.
type DeleteUserHandle interface {
	// Execute removes the external identity (e.g. Ory). It must be called
	// only after the caller has already deleted the database rows.
	Execute(ctx context.Context) error
}

func NewProvider(mode Mode, supa Provider, ory Provider) (Provider, error) {
	switch mode {
	case ModeSupabase:
		if supa == nil {
			return nil, fmt.Errorf("mode %q requires a supabase provider", mode)
		}

		return supa, nil
	case ModeOry:
		if ory == nil {
			return nil, fmt.Errorf("mode %q requires an ory provider", mode)
		}

		return ory, nil
	default:
		return nil, fmt.Errorf("unknown user profile provider mode %q", mode)
	}
}
