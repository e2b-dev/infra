package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

func (s *APIStore) requireAuthedTeamMatchesPath(c *gin.Context, teamID api.TeamID) (uuid.UUID, bool) {
	authTeamID := auth.MustGetTeamID(c)
	if authTeamID != teamID {
		s.sendAPIStoreError(c, http.StatusForbidden, "Team path parameter does not match authenticated team")

		return uuid.Nil, false
	}

	return authTeamID, true
}
