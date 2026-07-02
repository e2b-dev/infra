// Package provisioning owns the user/team bootstrap and creation sagas: turning
// an authenticated identity into a user with team memberships, self-serve team
// creation, and the SSO organization rules that constrain both. It orchestrates
// the auth DB, the identity provider (userprofile), and the billing sink
// (teamprovision — the outbound port this package drives, not to be confused
// with this package); it never touches HTTP.
package provisioning

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

const (
	baseTierID                   = "base_v1"
	maxTeamsPerUser              = 3
	maxTeamsPerUserWithProTier   = 10
	bootstrapProvisionRetryAge   = 30 * time.Second
	teamProvisionRollbackTimeout = 5 * time.Second
	creatorContextResolveTimeout = 2 * time.Second
)

type Service struct {
	authDB    *authdb.Client
	profiles  userprofile.Provider
	billing   internalteamprovision.TeamProvisionSink
	issuerURL string
}

func New(authDB *authdb.Client, profiles userprofile.Provider, billing internalteamprovision.TeamProvisionSink, issuerURL string) *Service {
	return &Service{
		authDB:    authDB,
		profiles:  profiles,
		billing:   billing,
		issuerURL: issuerURL,
	}
}

type ProvisionedTeam struct {
	ID            uuid.UUID
	Name          string
	Email         string
	Slug          string
	IsBlocked     bool
	BlockedReason *string
}

type bootstrapUserProfile struct {
	UserID          uuid.UUID
	Email           string
	DefaultTeamName string
	CreatorContext  *teamprovision.CreatorContextV1
}

type bootstrapUserIdentity struct {
	Issuer  string
	Subject string
}

type OIDCUserBootstrapInput struct {
	OIDCIssuer      string
	OIDCUserID      string
	OIDCUserEmail   string
	OIDCUserName    *string
	SignupIP        string
	SignupUserAgent string
}

// resolveProfile fetches a single user's profile through the configured profile
// provider, returning a 404 ProvisionError when the user is unknown.
func (s *Service) resolveProfile(ctx context.Context, userID uuid.UUID) (userprofile.Profile, error) {
	profiles, err := s.profiles.GetProfilesByUserID(ctx, []uuid.UUID{userID})
	if err != nil {
		return userprofile.Profile{}, fmt.Errorf("get user profile: %w", err)
	}

	profile, ok := profiles[userID]
	if !ok {
		return userprofile.Profile{}, &internalteamprovision.ProvisionError{
			StatusCode: http.StatusNotFound,
			Message:    "User not found",
		}
	}

	return profile, nil
}
