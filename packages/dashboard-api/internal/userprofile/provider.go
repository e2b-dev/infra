package userprofile

import (
	"context"

	"github.com/google/uuid"
)

type Profile struct {
	UserID uuid.UUID
	Email  string
}

type Provider interface {
	GetProfilesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error)
	FindProfilesByEmail(ctx context.Context, email string) ([]Profile, error)
}
