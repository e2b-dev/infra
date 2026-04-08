package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const baseTierID = "base_v1"
const maxTeamsPerUser = 3
const maxTeamsPerUserWithProTier = 10

type provisionedTeam struct {
	ID    uuid.UUID
	Name  string
	Email string
	Slug  string
}

func (s *APIStore) PostUsersBootstrap(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "bootstrap user")

	userID := auth.MustGetUserID(c)
	team, err := s.bootstrapUser(ctx, userID)
	if err != nil {
		s.handleProvisioningError(ctx, c, "bootstrap user", err)

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   team.ID,
		Slug: team.Slug,
	})
}

func (s *APIStore) PostTeams(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "create team")

	userID := auth.MustGetUserID(c)
	body, err := ginutils.ParseBody[api.CreateTeamRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	team, err := s.createTeam(ctx, userID, body.Name)
	if err != nil {
		s.handleProvisioningError(ctx, c, "create team", err)

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   team.ID,
		Slug: team.Slug,
	})
}

func (s *APIStore) bootstrapUser(ctx context.Context, userID uuid.UUID) (provisionedTeam, error) {
	authUser, err := s.authDB.Write.GetUser(ctx, userID)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("get auth user: %w", err)
	}

	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := txDB.UpsertPublicUser(ctx, queries.UpsertPublicUserParams{
		ID:    authUser.ID,
		Email: authUser.Email,
	}); err != nil {
		return provisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}

	existingTeam, err := txDB.GetDefaultTeamByUserID(ctx, userID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return provisionedTeam{}, fmt.Errorf("commit existing user bootstrap transaction: %w", err)
		}

		err = s.teamProvisionSink.ProvisionTeam(ctx, teamprovision.TeamBillingProvisionRequestedV1{
			TeamID:      existingTeam.ID,
			TeamName:    existingTeam.Name,
			TeamEmail:   existingTeam.Email,
			OwnerUserID: userID,
			Reason:      teamprovision.ReasonDefaultSignupTeam,
		})
		if err != nil {
			return provisionedTeam{}, err
		}

		return provisionedTeam{
			ID:    existingTeam.ID,
			Name:  existingTeam.Name,
			Email: existingTeam.Email,
			Slug:  existingTeam.Slug,
		}, nil
	}
	if !dberrors.IsNotFoundError(err) {
		return provisionedTeam{}, fmt.Errorf("get default team: %w", err)
	}

	team, err := txDB.CreateTeam(ctx, queries.CreateTeamParams{
		Name:  authUser.Email,
		Tier:  baseTierID,
		Email: authUser.Email,
	})
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("create default team: %w", err)
	}

	if err := txDB.CreateTeamMembership(ctx, queries.CreateTeamMembershipParams{
		UserID:    userID,
		TeamID:    team.ID,
		IsDefault: true,
		AddedBy:   nil,
	}); err != nil {
		return provisionedTeam{}, fmt.Errorf("create default team membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return provisionedTeam{}, fmt.Errorf("commit user bootstrap transaction: %w", err)
	}

	err = s.teamProvisionSink.ProvisionTeam(ctx, teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:      team.ID,
		TeamName:    team.Name,
		TeamEmail:   team.Email,
		OwnerUserID: userID,
		Reason:      teamprovision.ReasonDefaultSignupTeam,
	})
	if err != nil {
		if cleanupErr := s.cleanupCreatedTeam(ctx, team.ID); cleanupErr != nil {
			return provisionedTeam{}, fmt.Errorf("cleanup created default team: %w", cleanupErr)
		}

		return provisionedTeam{}, err
	}

	return provisionedTeam{
		ID:    team.ID,
		Name:  team.Name,
		Email: team.Email,
		Slug:  team.Slug,
	}, nil
}

func (s *APIStore) createTeam(ctx context.Context, userID uuid.UUID, name string) (provisionedTeam, error) {
	authUser, err := s.authDB.Write.GetUser(ctx, userID)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("get auth user: %w", err)
	}

	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := txDB.UpsertPublicUser(ctx, queries.UpsertPublicUserParams{
		ID:    authUser.ID,
		Email: authUser.Email,
	}); err != nil {
		return provisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}

	if _, err := txDB.LockUserTeamMembershipsForUpdate(ctx, userID); err != nil {
		return provisionedTeam{}, fmt.Errorf("lock user team memberships: %w", err)
	}

	if err := validateTeamCreationAllowed(ctx, txDB, userID); err != nil {
		return provisionedTeam{}, err
	}

	team, err := txDB.CreateTeam(ctx, queries.CreateTeamParams{
		Name:  name,
		Tier:  baseTierID,
		Email: authUser.Email,
	})
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("create team: %w", err)
	}

	if err := txDB.CreateTeamMembership(ctx, queries.CreateTeamMembershipParams{
		UserID:    userID,
		TeamID:    team.ID,
		IsDefault: false,
		AddedBy:   &userID,
	}); err != nil {
		return provisionedTeam{}, fmt.Errorf("create team membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return provisionedTeam{}, fmt.Errorf("commit team creation transaction: %w", err)
	}

	logger.L().Info(ctx, "team created locally",
		zap.String("user_id", userID.String()),
		zap.String("team_id", team.ID.String()),
		zap.String("reason", teamprovision.ReasonAdditionalTeam),
		zap.String("result", "created"),
	)

	err = s.teamProvisionSink.ProvisionTeam(ctx, teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:      team.ID,
		TeamName:    team.Name,
		TeamEmail:   team.Email,
		OwnerUserID: userID,
		Reason:      teamprovision.ReasonAdditionalTeam,
	})
	if err != nil {
		logger.L().Error(ctx, "team billing provisioning failed",
			zap.String("user_id", userID.String()),
			zap.String("team_id", team.ID.String()),
			zap.String("reason", teamprovision.ReasonAdditionalTeam),
			zap.String("result", "failed"),
			zap.Error(err),
		)
		if cleanupErr := s.cleanupCreatedTeam(ctx, team.ID); cleanupErr != nil {
			return provisionedTeam{}, fmt.Errorf("cleanup created team: %w", cleanupErr)
		}

		return provisionedTeam{}, err
	}

	logger.L().Info(ctx, "team billing provisioning succeeded",
		zap.String("user_id", userID.String()),
		zap.String("team_id", team.ID.String()),
		zap.String("reason", teamprovision.ReasonAdditionalTeam),
		zap.String("result", "provisioned"),
	)

	return provisionedTeam{
		ID:    team.ID,
		Name:  team.Name,
		Email: team.Email,
		Slug:  team.Slug,
	}, nil
}

func (s *APIStore) cleanupCreatedTeam(ctx context.Context, teamID uuid.UUID) error {
	if err := s.db.DeleteTeamByID(ctx, teamID); err != nil {
		logger.L().Error(ctx, "failed to cleanup created team",
			zap.String("teamID", teamID.String()),
			zap.String("result", "cleanup_failed"),
			zap.Error(err),
		)

		return err
	}

	if s.authService != nil {
		if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
			logger.L().Warn(ctx, "failed to invalidate team cache after cleanup",
				zap.String("teamID", teamID.String()),
				zap.Error(err),
			)
		}
	}

	logger.L().Info(ctx, "cleaned up created team",
		zap.String("teamID", teamID.String()),
		zap.String("result", "cleanup_succeeded"),
	)

	return nil
}

func validateTeamCreationAllowed(ctx context.Context, txDB *sqlcdb.Client, ownerUserID uuid.UUID) error {
	teams, err := txDB.GetTeamsWithUsersTeamsWithTierForUpdate(ctx, ownerUserID)
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

func (s *APIStore) handleProvisioningError(ctx context.Context, c *gin.Context, operation string, err error) {
	var provisionErr *internalteamprovision.ProvisionError
	if errors.As(err, &provisionErr) && provisionErr.IsBadRequest() {
		s.sendAPIStoreError(c, http.StatusBadRequest, provisionErr.Error())

		return
	}

	logger.L().Error(ctx, operation+" failed", zap.Error(err))
	s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to "+operation)
}
