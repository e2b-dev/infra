package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	teamDB "github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/user"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/usersteams"
)

func (a *APIStore) GetTeams(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	teamsDB, err := a.db.Client.Team.Query().
		Where(teamDB.HasUsersWith(user.ID(userID))).
		WithTeamAPIKeys().
		WithUsersTeams(func(query *models.UsersTeamsQuery) {
			query.Where(usersteams.UserID(userID))
		}).
		All(ctx)
	if err != nil {
		log.Println("Error when starting transaction: ", err)
		c.JSON(http.StatusInternalServerError, "Error when starting transaction")

		return
	}

	teams := make([]api.Team, len(teamsDB))
	for i, teamDB := range teamsDB {
		apiKey, err := team.CreateAPIKey(ctx, a.db, teamDB.ID, userID, "CLI login/configure")
		if err != nil {
			log.Println("Error when creating API key: ", err)
			c.JSON(http.StatusInternalServerError, "Error when creating API key")

			return
		}

		teams[i] = api.Team{
			TeamID:    teamDB.ID.String(),
			Name:      teamDB.Name,
			ApiKey:    apiKey.APIKey,
			IsDefault: teamDB.Edges.UsersTeams[0].IsDefault,
		}
	}
	c.JSON(http.StatusOK, teams)
}
