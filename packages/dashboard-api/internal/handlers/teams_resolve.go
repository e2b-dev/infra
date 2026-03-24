package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetTeamsResolve(c *gin.Context, params api.GetTeamsResolveParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "resolve team by slug")

	userID := auth.MustGetUserID(c)

	row, err := s.db.ResolveTeamBySlugAndUser(ctx, queries.ResolveTeamBySlugAndUserParams{
		UserID: userID,
		Slug:   params.Slug,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

			return
		}

		logger.L().Warn(ctx, "team resolve by slug failed", zap.Error(err), zap.String("slug", params.Slug))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to resolve team")

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   row.ID,
		Slug: row.Slug,
	})
}
