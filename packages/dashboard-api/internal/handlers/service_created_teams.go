package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
)

func (s *APIStore) PostInternalServiceCreatedTeams(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.ServiceCreatedTeamRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}

	name := strings.TrimSpace(body.Name)
	email := strings.TrimSpace(string(body.Email))
	if name == "" || email == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Team name and email are required")

		return
	}

	team, err := s.createServiceCreatedTeam(ctx, name, email)
	if err != nil {
		s.handleProvisioningError(ctx, c, "provision service-created team", err)

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   team.ID,
		Slug: team.Slug,
	})
}
