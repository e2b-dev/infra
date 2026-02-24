package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func TestGetMe(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	t.Run("authenticated", func(t *testing.T) {
		t.Parallel()

		// data
		teamID := uuid.New()

		// setup
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		a := APIStore{}

		// push team info into context
		team := &types.Team{
			Team: &authqueries.Team{
				ID:   teamID,
				Name: "test-team",
			},
		}
		c.Set(auth.TeamContextKey, team)

		// run test
		a.GetMe(c)

		// verify results
		assert.Equal(t, http.StatusOK, w.Code)
		var response api.TokenInfo
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		require.NotNil(t, response.TeamID)
		assert.Equal(t, teamID, response.TeamID)
		assert.Equal(t, team.Team.Name, response.TeamName)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		t.Parallel()

		// setup
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		a := APIStore{}

		// run test
		a.GetMe(c)

		// verify results
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		var response gin.H
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, gin.H{"code": float64(401), "message": "no credentials found"}, response)
	})
}
