package userprofile

import (
	"context"
	"fmt"

	"github.com/google/uuid"
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
	case ModeSupabaseOryFallback:
		if supa == nil || ory == nil {
			return nil, fmt.Errorf("mode %q requires both supabase and ory providers", mode)
		}

		return newDualProvider(supa, ory), nil
	default:
		return nil, fmt.Errorf("unknown user profile provider mode %q", mode)
	}
}
