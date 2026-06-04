package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTeamsTeamIDMembers(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list team members")

	authTeamID, ok := s.requireAuthedTeamMatchesPath(c, teamID)
	if !ok {
		return
	}

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(authTeamID.String()))

	rows, err := s.db.GetTeamMembers(ctx, authTeamID)
	if err != nil {
		logger.L().Error(ctx, "failed to get team members", zap.Error(err), logger.WithTeamID(authTeamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get team members")

		return
	}

	userIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		userIDs = append(userIDs, row.UserID)
	}

	profiles, err := s.userProfiles.GetProfilesByUserID(ctx, userIDs)
	if err != nil {
		logger.L().Error(ctx, "failed to get member profiles", zap.Error(err), logger.WithTeamID(authTeamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get team member profiles")

		return
	}

	members := make([]api.TeamMember, 0, len(rows))
	for _, row := range rows {
		profile, ok := profiles[row.UserID]
		if !ok || profile.Email == "" {
			logger.L().Warn(ctx, "team member has missing profile", logger.WithTeamID(authTeamID.String()), logger.WithUserID(row.UserID.String()))

			continue
		}

		member := api.TeamMember{
			Id:        row.UserID,
			Email:     profile.Email,
			IsDefault: row.IsDefault,
			AddedBy:   row.AddedBy,
			Providers: profile.Providers,
		}
		if member.Providers == nil {
			member.Providers = []string{}
		}
		if profile.Name != "" {
			member.Name = new(profile.Name)
		}
		if profile.ProfilePictureURL != "" {
			member.ProfilePictureUrl = new(profile.ProfilePictureURL)
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

	authTeamID, ok := s.requireAuthedTeamMatchesPath(c, teamID)
	if !ok {
		return
	}

	userID := auth.MustGetUserID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(authTeamID.String()))

	body, err := ginutils.ParseBody[api.AddTeamMemberRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	profiles, err := s.userProfiles.FindProfilesByEmail(ctx, string(body.Email))
	if err != nil {
		logger.L().Error(ctx, "failed to look up user by email", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to look up user")

		return
	}

	if len(profiles) == 0 {
		s.sendAPIStoreError(c, http.StatusNotFound, "User with this email does not exist. Please ask them to sign up first.")

		return
	}

	if len(profiles) > 1 {
		logger.L().Error(ctx, "ambiguous user email lookup", zap.Int("matches", len(profiles)))
		s.sendAPIStoreError(c, http.StatusConflict, "Multiple users with this email exist. Please contact support.")

		return
	}

	user := profiles[0]
	if err := s.authDB.Write.UpsertPublicUser(ctx, user.UserID); err != nil {
		logger.L().Error(ctx, "failed to create public user anchor", zap.Error(err), logger.WithUserID(user.UserID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to add team member")

		return
	}

	err = s.db.AddTeamMember(ctx, queries.AddTeamMemberParams{
		UserID:  user.UserID,
		TeamID:  authTeamID,
		AddedBy: userID,
	})
	if err != nil {
		if dberrors.IsUniqueConstraintViolation(err) {
			s.sendAPIStoreError(c, http.StatusBadRequest, "User is already a member of this team")

			return
		}

		logger.L().Error(ctx, "failed to add team member", zap.Error(err), logger.WithTeamID(authTeamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to add team member")

		return
	}

	s.authService.InvalidateTeamMemberCache(ctx, user.UserID, authTeamID.String())

	c.Status(http.StatusCreated)
}

func (s *APIStore) DeleteTeamsTeamIDMembersUserId(c *gin.Context, teamID api.TeamID, userId api.UserId) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "remove team member")

	authTeamID, ok := s.requireAuthedTeamMatchesPath(c, teamID)
	if !ok {
		return
	}

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(authTeamID.String()))

	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		logger.L().Error(ctx, "failed to start transaction for removing team member", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to remove team member")

		return
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	lockedMembers, err := txDB.LockTeamMembersForUpdate(ctx, authTeamID)
	if err != nil {
		logger.L().Error(ctx, "failed to lock team members", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to check team members")

		return
	}

	relation, err := txDB.GetTeamMemberRelation(ctx, queries.GetTeamMemberRelationParams{
		TeamID: authTeamID,
		UserID: userId,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
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

	if len(lockedMembers) <= 1 {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Cannot remove the last team member")

		return
	}

	err = txDB.RemoveTeamMember(ctx, queries.RemoveTeamMemberParams{
		TeamID: authTeamID,
		UserID: userId,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to remove team member", zap.Error(err), logger.WithTeamID(authTeamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to remove team member")

		return
	}

	if err := tx.Commit(ctx); err != nil {
		logger.L().Error(ctx, "failed to commit team member removal", zap.Error(err), logger.WithTeamID(authTeamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to remove team member")

		return
	}

	s.authService.InvalidateTeamMemberCache(ctx, userId, authTeamID.String())

	c.Status(http.StatusNoContent)
}
