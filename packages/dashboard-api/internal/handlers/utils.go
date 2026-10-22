package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *APIStore) requireTemplateAccess(c *gin.Context, templateID api.TemplateID, teamID uuid.UUID) bool {
	ctx := c.Request.Context()

	_, err := s.db.GetTeamTemplate(ctx, queries.GetTeamTemplateParams{
		ID:     templateID,
		TeamID: teamID,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Template not found")

			return false
		}

		logger.L().Error(ctx, "Error getting template", zap.Error(err), logger.WithTeamID(teamID.String()), logger.WithTemplateID(templateID))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template")

		return false
	}

	return true
}
