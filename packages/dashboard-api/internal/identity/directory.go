package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrUnknownIssuer = errors.New("no identity directory registered for issuer")

var ErrUserNotFound = errors.New("user not found")

var ErrIdentityNotFound = errors.New("identity not found")

// Directory is the subject-keyed admin API of a single identity provider
// (e.g. one Ory project). It never touches the database; issuer routing is the
// Service's concern.
type Directory interface {
	GetIdentity(ctx context.Context, subject string) (Identity, error)
	ListIdentities(ctx context.Context, subjects []string) ([]Identity, error)
	SearchByEmail(ctx context.Context, email string) ([]Identity, error)
	SetExternalID(ctx context.Context, subject string, externalID uuid.UUID) error
	DeleteIdentity(ctx context.Context, subject string) error
}
