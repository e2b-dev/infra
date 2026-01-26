package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetTeams(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	results, err := a.authDB.Read.GetTeamsWithUsersTeams(ctx, userID)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when getting teams", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when starting transaction")

		return
	}

	teams := make([]api.Team, len(results))
	for i, row := range results {
		// We create a new API key for the CLI and backwards compatibility with API Keys hashing
		apiKey, err := team.CreateAPIKey(ctx, a.authDB, row.Team.ID, userID, "CLI login/configure")
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when creating team API key", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when creating team API key")

			return
		}

		teams[i] = api.Team{
			TeamID:    row.Team.ID.String(),
			Name:      row.Team.Name,
			ApiKey:    apiKey.RawAPIKey,
			IsDefault: row.UsersTeam.IsDefault,
		}
	}

	c.JSON(http.StatusOK, teams)
}
