package provisioning

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

// enrollSSOMember adds the user to their SSO organization's auto-join teams and
// returns the team the response lands on. It fails closed when the org has no
// auto-join team, since every SSO org must have at least one — that state is a
// misconfiguration. No billing is emitted; SSO teams are provisioned out of band.
// The caller must hold the per-user lock.
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
		if err := authTxDB.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
			UserID:    userID,
			TeamID:    row.Team.ID,
			IsDefault: false,
			AddedBy:   nil,
		}); err != nil {
			return ProvisionedTeam{}, fmt.Errorf("create sso team membership: %w", err)
		}
	}

	// Land on the earliest auto-join team (the query orders by created_at).
	landing := autoTeams[0].Team

	return ProvisionedTeam{
		ID:            landing.ID,
		Name:          landing.Name,
		Email:         landing.Email,
		Slug:          landing.Slug,
		IsBlocked:     landing.IsBlocked,
		BlockedReason: landing.BlockedReason,
	}, nil
}

// ensureNotSSOManaged blocks team creation for SSO-managed users; their team
// membership is driven entirely by their identity provider.
func (s *Service) ensureNotSSOManaged(ctx context.Context, userID uuid.UUID) error {
	orgID, err := s.profiles.GetUserOrganizationID(ctx, userID)
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

// ValidateInviteeOrganization rejects adding a user to an SSO-managed team when
// the invitee's Ory identity belongs to a different organization.
func (s *Service) ValidateInviteeOrganization(ctx context.Context, teamOrgID, inviteeUserID uuid.UUID) error {
	inviteeOrgID, err := s.profiles.GetUserOrganizationID(ctx, inviteeUserID)
	if err != nil {
		return fmt.Errorf("resolve invitee organization: %w", err)
	}

	if inviteeOrgID != teamOrgID {
		return &internalteamprovision.ProvisionError{
			StatusCode: http.StatusForbidden,
			Message:    "Only accounts from your organization can be added to this team.",
		}
	}

	return nil
}
