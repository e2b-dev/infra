package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

func (s *APIStore) requireAuthedTeamMatchesPath(c *gin.Context, teamID api.TeamID) (*authtypes.Team, bool) {
	teamInfo := auth.MustGetTeamInfo(c)
	if teamInfo.Team.ID != teamID {
		s.sendAPIStoreError(c, http.StatusForbidden, "Team path parameter does not match authenticated team")

		return nil, false
	}

	return teamInfo, true
}
