package provisioning

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func (s *Service) enrollSSOMember(ctx context.Context, authTxDB *authqueries.Queries, userID, orgID uuid.UUID) (ProvisionedTeam, error) {
	autoTeams, err := authTxDB.GetAutoJoinTeamsBySSOOrganizationID(ctx, orgID)
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("get auto-join teams for sso organization: %w", err)
	}
	if len(autoTeams) == 0 {
		return ProvisionedTeam{}, &internalteamprovision.ProvisionError{
			StatusCode: http.StatusForbidden,
			Message:    "Your organization's SSO is not fully set up yet. Please contact support.",
		}
	}

	for _, row := range autoTeams {
		if err := authTxDB.CreateTeamMembershipIfMissing(ctx, authqueries.CreateTeamMembershipIfMissingParams{
			UserID:    userID,
			TeamID:    row.Team.ID,
			IsDefault: false,
			AddedBy:   nil,
		}); err != nil {
			return ProvisionedTeam{}, fmt.Errorf("create sso team membership: %w", err)
		}
	}

	landing := autoTeams[0].Team

	return newProvisionedTeam(landing.ID, landing.Name, landing.Email, landing.Slug, landing.IsBlocked, landing.BlockedReason, userID), nil
}

func (s *Service) ensureNotSSOManaged(ctx context.Context, userID uuid.UUID) error {
	orgID, err := s.identityService.UserOrganizationID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve sso organization: %w", err)
	}
	if orgID != uuid.Nil {
		return &internalteamprovision.ProvisionError{
			StatusCode: http.StatusForbidden,
			Message:    "SSO-managed accounts can't create teams. Contact your organization admin.",
		}
	}

	return nil
}
