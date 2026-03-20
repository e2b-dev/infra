package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTeamsTeamIDMembers(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list team members")

	teamInfo, ok := s.requireAuthedTeamMatchesPath(c, teamID)
	if !ok {
		return
	}

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamInfo.Team.ID.String()))

	rows, err := s.db.GetTeamMembers(ctx, teamInfo.Team.ID)
	if err != nil {
		logger.L().Error(ctx, "failed to get team members", zap.Error(err), logger.WithTeamID(teamInfo.Team.ID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get team members")

		return
	}

	members := make([]api.TeamMember, 0, len(rows))
	for _, row := range rows {
		member := api.TeamMember{
			Id:        row.UserID,
			Email:     row.Email,
			IsDefault: row.IsDefault,
			AddedBy:   row.AddedBy,
		}

		if row.CreatedAt.Valid {
			t := row.CreatedAt.Time.UTC()
			member.CreatedAt = &t
		}

		members = append(members, member)
	}

	c.JSON(http.StatusOK, api.TeamMembersResponse{
		Members: members,
	})
}

func (s *APIStore) PostTeamsTeamIDMembers(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "add team member")

	teamInfo, ok := s.requireAuthedTeamMatchesPath(c, teamID)
	if !ok {
		return
	}

	userID := auth.MustGetUserID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamInfo.Team.ID.String()))

	var body api.AddTeamMemberRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	user, err := s.db.GetUserByEmail(ctx, string(body.Email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.sendAPIStoreError(c, http.StatusNotFound, "User with this email does not exist. Please ask them to sign up first.")

			return
		}

		logger.L().Error(ctx, "failed to look up user by email", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to look up user")

		return
	}

	addedRows, err := s.db.AddTeamMember(ctx, queries.AddTeamMemberParams{
		UserID:  user.ID,
		TeamID:  teamInfo.Team.ID,
		AddedBy: userID,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to add team member", zap.Error(err), logger.WithTeamID(teamInfo.Team.ID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to add team member")

		return
	}
	if status, message, ok := mapAddTeamMemberRows(addedRows); ok {
		s.sendAPIStoreError(c, status, message)

		return
	}

	c.Status(http.StatusCreated)
}

func (s *APIStore) DeleteTeamsTeamIDMembersUserId(c *gin.Context, teamID api.TeamID, userId api.UserId) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "remove team member")

	teamInfo, ok := s.requireAuthedTeamMatchesPath(c, teamID)
	if !ok {
		return
	}

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamInfo.Team.ID.String()))

	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		logger.L().Error(ctx, "failed to start transaction for removing team member", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to remove team member")

		return
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	relation, err := txDB.GetTeamMemberRelation(ctx, queries.GetTeamMemberRelationParams{
		TeamID: teamInfo.Team.ID,
		UserID: userId,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.sendAPIStoreError(c, http.StatusBadRequest, "User is not a member of this team")

			return
		}

		logger.L().Error(ctx, "failed to get team member relation", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get team member")

		return
	}

	if relation.IsDefault {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Cannot remove a default team member")

		return
	}

	lockedMembers, err := txDB.LockTeamMembersForUpdate(ctx, teamInfo.Team.ID)
	if err != nil {
		logger.L().Error(ctx, "failed to lock team members", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to check team members")

		return
	}

	if len(lockedMembers) <= 1 {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Cannot remove the last team member")

		return
	}

	removedRows, err := txDB.RemoveTeamMember(ctx, queries.RemoveTeamMemberParams{
		TeamID: teamInfo.Team.ID,
		UserID: userId,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to remove team member", zap.Error(err), logger.WithTeamID(teamInfo.Team.ID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to remove team member")

		return
	}
	if status, message, ok := mapRemoveTeamMemberRows(removedRows); ok {
		s.sendAPIStoreError(c, status, message)

		return
	}

	if err := tx.Commit(ctx); err != nil {
		logger.L().Error(ctx, "failed to commit team member removal", zap.Error(err), logger.WithTeamID(teamInfo.Team.ID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to remove team member")

		return
	}

	c.Status(http.StatusNoContent)
}

func mapAddTeamMemberRows(addedRows int64) (int, string, bool) {
	if addedRows == 0 {
		return http.StatusBadRequest, "User is already a member of this team", true
	}

	return 0, "", false
}

func mapRemoveTeamMemberRows(removedRows int64) (int, string, bool) {
	if removedRows == 0 {
		return http.StatusBadRequest, "User is not a member of this team", true
	}

	return 0, "", false
}
