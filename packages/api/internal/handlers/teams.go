package handlers

import (
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/user"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
)

func (a *APIStore) GetTeams(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	teamsDB, err := a.db.Client.Team.Query().Where(team.HasUsersWith(user.ID(userID))).WithTeamAPIKeys().All(ctx)
	if err != nil {
		log.Println("Error when starting transaction: ", err)
		c.String(http.StatusInternalServerError, "Error when starting transaction")

		return
	}

	teams := make([]api.Team, len(teamsDB))
	for i, teamDB := range teamsDB {
		teams[i] = api.Team{
			TeamID: teamDB.ID.String(),
			Name:   teamDB.Name,
			ApiKey: teamDB.Edges.TeamAPIKeys[0].ID,
		}
	}
	c.JSON(http.StatusOK, teams)
}
