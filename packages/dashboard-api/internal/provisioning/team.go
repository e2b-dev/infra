package provisioning

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func (s *Service) CreateTeam(ctx context.Context, userID uuid.UUID, name string) (ProvisionedTeam, error) {
	if err := s.ensureNotSSOManaged(ctx, userID); err != nil {
		return ProvisionedTeam{}, err
	}

	profile, err := s.resolveProfile(ctx, userID)
	if err != nil {
		return ProvisionedTeam{}, err
	}

	authTxDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := authTxDB.UpsertPublicUser(ctx, userID); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}

	// Serialize team creation even when the user currently has no team memberships.
	if _, err := authTxDB.LockPublicUserForUpdate(ctx, userID); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("lock public user: %w", err)
	}

	if err := validateTeamCreationAllowed(ctx, authTxDB, userID); err != nil {
		return ProvisionedTeam{}, err
	}

	team, err := authTxDB.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          name,
		Tier:          baseTierID,
		Email:         profile.Email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("create team: %w", err)
	}

	if err := authTxDB.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
		UserID:    userID,
		TeamID:    team.ID,
		IsDefault: false,
		AddedBy:   &userID,
	}); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("create team membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("commit team creation transaction: %w", err)
	}

	req := teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:         team.ID,
		TeamName:       team.Name,
		TeamEmail:      team.Email,
		CreatorUserID:  userID,
		CreatorContext: s.resolveTeamCreatorContext(ctx, userID),
		Reason:         teamprovision.ReasonAdditionalTeam,
	}
	if err := s.billing.ProvisionTeam(ctx, req); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teamProvisionRollbackTimeout)
		defer cancel()

		if deleteErr := s.authDB.Write.DeleteTeamByID(rollbackCtx, team.ID); deleteErr != nil {
			return ProvisionedTeam{}, fmt.Errorf("delete team after provisioning failure: provision=%s delete=%w", err.Error(), deleteErr)
		}

		return ProvisionedTeam{}, err
	}

	return ProvisionedTeam{
		ID:            team.ID,
		Name:          team.Name,
		Email:         team.Email,
		Slug:          team.Slug,
		IsBlocked:     team.IsBlocked,
		BlockedReason: team.BlockedReason,
	}, nil
}

func (s *Service) BootstrapTeam(ctx context.Context, name string, email string) (ProvisionedTeam, error) {
	team, err := s.authDB.Write.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          name,
		Tier:          baseTierID,
		Email:         email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("create team: %w", err)
	}

	req := teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:        team.ID,
		TeamName:      team.Name,
		TeamEmail:     team.Email,
		CreatorUserID: uuid.Nil,
		Reason:        teamprovision.ReasonAdditionalTeam,
	}
	if err := s.billing.ProvisionTeam(ctx, req); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teamProvisionRollbackTimeout)
		defer cancel()

		if deleteErr := s.authDB.Write.DeleteTeamByID(rollbackCtx, team.ID); deleteErr != nil {
			return ProvisionedTeam{}, fmt.Errorf("delete team after provisioning failure: provision=%s delete=%w", err.Error(), deleteErr)
		}

		return ProvisionedTeam{}, err
	}

	return ProvisionedTeam{
		ID:            team.ID,
		Name:          team.Name,
		Email:         team.Email,
		Slug:          team.Slug,
		IsBlocked:     team.IsBlocked,
		BlockedReason: team.BlockedReason,
	}, nil
}

func validateTeamCreationAllowed(ctx context.Context, authTxDB *authqueries.Queries, ownerUserID uuid.UUID) error {
	teams, err := authTxDB.GetTeamsWithUsersTeamsWithTierForUpdate(ctx, ownerUserID)
	if err != nil {
		return fmt.Errorf("query user teams for limit check: %w", err)
	}

	hasProTier := false
	for _, row := range teams {
		if row.Tier != baseTierID {
			hasProTier = true
		}
		if row.IsBanned {
			return &internalteamprovision.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message:    "You're unable to create a team right now. Please contact support if this persists.",
			}
		}
	}

	if hasProTier {
		if len(teams) >= maxTeamsPerUserWithProTier {
			return &internalteamprovision.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message:    fmt.Sprintf("You can't create more than %d teams", maxTeamsPerUserWithProTier),
			}
		}
	} else {
		if len(teams) >= maxTeamsPerUser {
			return &internalteamprovision.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message: fmt.Sprintf(
					"You can't create more than %d teams, you can upgrade to Pro tier to create up to %d teams",
					maxTeamsPerUser,
					maxTeamsPerUserWithProTier,
				),
			}
		}
	}

	return nil
}
