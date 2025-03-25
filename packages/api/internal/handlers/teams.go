package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/team"
)

func (a *APIStore) GetTeams(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	results, err := a.sqlcDB.TeamsListForUser(ctx, userID)
	if err != nil {
		log.Println("Error when starting transaction: ", err)
		c.JSON(http.StatusInternalServerError, "Error when starting transaction")

		return
	}

	teams := make([]api.Team, len(results))
	for i, row := range results {

		apiKey, err := team.CreateAPIKey(ctx, a.db, row.Team.ID, userID, "CLI login/configure")
		if err != nil {
			log.Println("Error when creating API key: ", err)
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
