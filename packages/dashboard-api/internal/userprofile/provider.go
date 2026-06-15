package userprofile

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

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
