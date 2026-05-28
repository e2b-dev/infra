package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) PostAdminUsersUserIdBootstrap(c *gin.Context, userId api.UserId) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "bootstrap user")

	team, err := s.bootstrapSupabaseUser(ctx, userId)
	if err != nil {
		s.handleProvisioningError(ctx, c, "bootstrap user", err)

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   team.ID,
		Slug: team.Slug,
	})
}
