package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/teambilling"
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

	err = s.teamProvisionSink.ProvisionTeam(ctx, teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:      team.ID,
		TeamName:    team.Name,
		TeamEmail:   team.Email,
		OwnerUserID: userID,
		Reason:      teamprovision.ReasonAdditionalTeam,
	})
	if err != nil {
		if cleanupErr := s.cleanupCreatedTeam(ctx, team.ID); cleanupErr != nil {
			return provisionedTeam{}, fmt.Errorf("cleanup created team: %w", cleanupErr)
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

func (s *APIStore) cleanupCreatedTeam(ctx context.Context, teamID uuid.UUID) error {
	if err := s.db.DeleteTeamByID(ctx, teamID); err != nil {
		logger.L().Error(ctx, "failed to cleanup created team",
			zap.String("teamID", teamID.String()),
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

	return nil
}

func (s *APIStore) handleProvisioningError(ctx context.Context, c *gin.Context, operation string, err error) {
	var provisionErr *teambilling.ProvisionError
	if errors.As(err, &provisionErr) && provisionErr.IsBadRequest() {
		s.sendAPIStoreError(c, http.StatusBadRequest, provisionErr.Error())

		return
	}

	logger.L().Error(ctx, operation+" failed", zap.Error(err))
	s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to "+operation)
}
