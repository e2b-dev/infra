package auth

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

// SetUserIDForTest sets the user ID on the gin context for use in tests.
func SetUserIDForTest(t *testing.T, c *gin.Context, userID uuid.UUID) {
	t.Helper()

	setUserID(c, userID)
}

// SetTeamInfoForTest sets the team info on the gin context for use in tests.
func SetTeamInfoForTest(t *testing.T, c *gin.Context, team *types.Team) {
	t.Helper()

	setTeamInfo(c, team)
}
