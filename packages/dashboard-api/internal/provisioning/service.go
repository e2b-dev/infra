package provisioning

import (
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/identity"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
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
	authDB          *authdb.Client
	identityService identity.Service
	billing         internalteamprovision.TeamProvisionSink
}

func New(authDB *authdb.Client, identityService identity.Service, billing internalteamprovision.TeamProvisionSink) *Service {
	return &Service{
		authDB:          authDB,
		identityService: identityService,
		billing:         billing,
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

func newProvisionedTeam(id uuid.UUID, name, email, slug string, isBlocked bool, blockedReason *string) ProvisionedTeam {
	return ProvisionedTeam{
		ID:            id,
		Name:          name,
		Email:         email,
		Slug:          slug,
		IsBlocked:     isBlocked,
		BlockedReason: blockedReason,
	}
}
