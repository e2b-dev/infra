package provisioning

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/identity"
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
	if err := s.provisionBillingOrDeleteTeam(ctx, team.ID, req); err != nil {
		return ProvisionedTeam{}, err
	}

	return newProvisionedTeam(team.ID, team.Name, team.Email, team.Slug, team.IsBlocked, team.BlockedReason), nil
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
	if err := s.provisionBillingOrDeleteTeam(ctx, team.ID, req); err != nil {
		return ProvisionedTeam{}, err
	}

	return newProvisionedTeam(team.ID, team.Name, team.Email, team.Slug, team.IsBlocked, team.BlockedReason), nil
}

func (s *Service) resolveProfile(ctx context.Context, userID uuid.UUID) (identity.Profile, error) {
	profiles, err := s.idp.GetProfilesByUserID(ctx, []uuid.UUID{userID})
	if err != nil {
		return identity.Profile{}, fmt.Errorf("get user profile: %w", err)
	}

	profile, ok := profiles[userID]
	if !ok {
		return identity.Profile{}, &internalteamprovision.ProvisionError{
			StatusCode: http.StatusNotFound,
			Message:    "User not found",
		}
	}

	return profile, nil
}

func (s *Service) provisionBillingOrDeleteTeam(ctx context.Context, teamID uuid.UUID, req teamprovision.TeamBillingProvisionRequestedV1) error {
	if err := s.billing.ProvisionTeam(ctx, req); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teamProvisionRollbackTimeout)
		defer cancel()

		if deleteErr := s.authDB.Write.DeleteTeamByID(rollbackCtx, teamID); deleteErr != nil {
			return fmt.Errorf("delete team after provisioning failure: provision=%s delete=%w", err.Error(), deleteErr)
		}

		return err
	}

	return nil
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

	teamLimit := maxTeamsPerUser
	limitMessage := fmt.Sprintf(
		"You can't create more than %d teams, you can upgrade to Pro tier to create up to %d teams",
		maxTeamsPerUser,
		maxTeamsPerUserWithProTier,
	)
	if hasProTier {
		teamLimit = maxTeamsPerUserWithProTier
		limitMessage = fmt.Sprintf("You can't create more than %d teams", maxTeamsPerUserWithProTier)
	}

	if len(teams) >= teamLimit {
		return &internalteamprovision.ProvisionError{
			StatusCode: http.StatusBadRequest,
			Message:    limitMessage,
		}
	}

	return nil
}
