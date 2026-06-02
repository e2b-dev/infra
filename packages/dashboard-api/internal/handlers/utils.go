package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *APIStore) requireAuthedTeamMatchesPath(c *gin.Context, teamID api.TeamID) (uuid.UUID, bool) {
	authTeamID := auth.MustGetTeamID(c)
	if authTeamID != teamID {
		s.sendAPIStoreError(c, http.StatusForbidden, "Team path parameter does not match authenticated team")

		return uuid.Nil, false
	}

	return authTeamID, true
}

func (s *APIStore) requireTemplateAccess(c *gin.Context, templateID api.TemplateID, teamID uuid.UUID) bool {
	ctx := c.Request.Context()

	template, err := s.db.GetTemplateByIDWithAliases(ctx, templateID)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Template not found")

			return false
		}

		logger.L().Error(ctx, "Error getting template", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithTemplateID(templateID))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")

		return false
	}

	if template.TeamID != teamID {
		s.sendAPIStoreError(c, http.StatusNotFound, "Template not found")

		return false
	}

	return true
}
