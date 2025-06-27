package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/team"
)

func (a *APIStore) GetTeams(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	results, err := a.sqlcDB.GetTeamsWithUsersTeams(ctx, userID)
	if err != nil {
		zap.L().Error("error when starting transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, "Error when starting transaction")

		return
	}

	teams := make([]api.Team, len(results))
	for i, row := range results {
		// We create a new API key for the CLI and backwards compatibility with API Keys hashing
		apiKey, err := team.CreateAPIKey(ctx, a.db, row.Team.ID, userID, "CLI login/configure")
		if err != nil {
			zap.L().Error("error when creating API key", zap.Error(err))
			c.JSON(http.StatusInternalServerError, "Error when creating API key")

			return
		}

		teams[i] = api.Team{
			TeamID:    row.Team.ID.String(),
			Name:      row.Team.Name,
			ApiKey:    apiKey.APIKey,
			IsDefault: row.UsersTeam.IsDefault,
		}
	}

	c.JSON(http.StatusOK, teams)
}
