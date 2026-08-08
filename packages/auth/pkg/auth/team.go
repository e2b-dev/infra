package auth

import (
	"github.com/gin-gonic/gin"

	internalauthteam "github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/team"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

type BlockedTeamAllowlist = internalauthteam.BlockedTeamAllowlist

func CheckTeamBlocked(team *types.Team) error {
	return internalauthteam.CheckTeamBlocked(team)
}

func CheckTeamAccess(c *gin.Context, team *types.Team, allowlist BlockedTeamAllowlist) error {
	return internalauthteam.CheckTeamAccess(c, team, allowlist)
}

func EnforceBlockedTeam(allowlist BlockedTeamAllowlist) gin.HandlerFunc {
	return internalauthteam.EnforceBlockedTeam(allowlist)
}
