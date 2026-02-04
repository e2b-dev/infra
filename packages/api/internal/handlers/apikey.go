package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PatchApiKeysApiKeyID(c *gin.Context, apiKeyID string) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.UpdateTeamAPIKey](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	apiKeyIDParsed, err := uuid.Parse(apiKeyID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing API key ID: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing API key ID", err)

		return
	}

	teamID := a.GetTeamInfo(c).Team.ID

	now := time.Now()
	_, err = a.authDB.Write.UpdateTeamApiKey(ctx, authqueries.UpdateTeamApiKeyParams{
		Name:      body.Name,
		UpdatedAt: &now,
		ID:        apiKeyIDParsed,
		TeamID:    teamID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		c.String(http.StatusNotFound, "id not found")

		return
	} else if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating team API key name: %s", err))

		telemetry.ReportCriticalError(ctx, "error when updating team API key name", err)

		return
	}

	c.Status(http.StatusAccepted)
}

func (a *APIStore) GetApiKeys(c *gin.Context) {
	ctx := c.Request.Context()

	teamID := a.GetTeamInfo(c).Team.ID

	apiKeysDB, err := a.authDB.Read.GetTeamAPIKeysWithCreator(ctx, teamID)
	if err != nil {
		logger.L().Warn(ctx, "error when getting team API keys", zap.Error(err))
		c.String(http.StatusInternalServerError, "Error when getting team API keys")

		return
	}

	teamAPIKeys := make([]api.TeamAPIKey, len(apiKeysDB))
	for i, apiKey := range apiKeysDB {
		var createdBy *api.TeamUser
		if apiKey.CreatedByID != nil && apiKey.CreatedByEmail != nil {
			createdBy = &api.TeamUser{
				Email: *apiKey.CreatedByEmail,
				Id:    *apiKey.CreatedByID,
			}
		}

		teamAPIKeys[i] = api.TeamAPIKey{
			Id:   apiKey.ID,
			Name: apiKey.Name,
			Mask: api.IdentifierMaskingDetails{
				Prefix:            apiKey.ApiKeyPrefix,
				ValueLength:       int(apiKey.ApiKeyLength),
				MaskedValuePrefix: apiKey.ApiKeyMaskPrefix,
				MaskedValueSuffix: apiKey.ApiKeyMaskSuffix,
			},
			CreatedAt: apiKey.CreatedAt,
			CreatedBy: createdBy,
			LastUsed:  apiKey.LastUsed,
		}
	}
	c.JSON(http.StatusOK, teamAPIKeys)
}

func (a *APIStore) DeleteApiKeysApiKeyID(c *gin.Context, apiKeyID string) {
	ctx := c.Request.Context()

	apiKeyIDParsed, err := uuid.Parse(apiKeyID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing API key ID: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing API key ID", err)

		return
	}

	teamID := a.GetTeamInfo(c).Team.ID

	ids, err := a.authDB.Write.DeleteTeamAPIKey(ctx, authqueries.DeleteTeamAPIKeyParams{
		ID:     apiKeyIDParsed,
		TeamID: teamID,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting API key: %s", err))

		telemetry.ReportCriticalError(ctx, "error when deleting API key", err)

		return
	}
	if len(ids) == 0 {
		c.String(http.StatusNotFound, "id not found")

		return
	}

	c.Status(http.StatusNoContent)
}

func (a *APIStore) PostApiKeys(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)
	teamID := a.GetTeamInfo(c).Team.ID

	body, err := utils.ParseBody[api.NewTeamAPIKey](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	apiKey, err := team.CreateAPIKey(ctx, a.authDB, teamID, userID, body.Name)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating team API key: %s", err))

		telemetry.ReportCriticalError(ctx, "error when creating team API key", err)

		return
	}

	user, err := a.authDB.Read.GetUser(ctx, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return
	}

	c.JSON(http.StatusCreated, api.CreatedTeamAPIKey{
		Id:   apiKey.ID,
		Name: apiKey.Name,
		Key:  apiKey.RawAPIKey,
		Mask: api.IdentifierMaskingDetails{
			Prefix:            apiKey.ApiKeyPrefix,
			ValueLength:       int(apiKey.ApiKeyLength),
			MaskedValuePrefix: apiKey.ApiKeyMaskPrefix,
			MaskedValueSuffix: apiKey.ApiKeyMaskSuffix,
		},
		CreatedBy: &api.TeamUser{
			Id:    user.ID,
			Email: user.Email,
		},
		CreatedAt: apiKey.CreatedAt,
		LastUsed:  apiKey.LastUsed,
	})
}
