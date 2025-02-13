package handlers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PatchApikeysApiKeyID(c *gin.Context, apiKeyID string) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.UpdateTeamAPIKey](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	apiKeyIDParsed, err := uuid.Parse(apiKeyID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing API key ID: %s", err))

		errMsg := fmt.Errorf("error when parsing API key ID: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	err = a.db.Client.TeamAPIKey.UpdateOneID(apiKeyIDParsed).SetName(body.Name).SetUpdatedAt(time.Now()).Exec(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating team API key name: %s", err))

		errMsg := fmt.Errorf("error when updating team API key name: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	c.Status(http.StatusAccepted)
}

func (a *APIStore) GetApikeys(c *gin.Context) {
	ctx := c.Request.Context()

	teamID := a.GetTeamInfo(c).Team.ID

	apiKeys, err := a.db.GetTeamAPIKeys(ctx, teamID)
	if err != nil {
		log.Println("Error when getting team API keys: ", err)
		c.JSON(http.StatusInternalServerError, "Error when getting team API keys")

		return
	}

	teamAPIKeys := make([]api.TeamAPIKey, len(apiKeys))
	for i, apiKey := range apiKeys {
		teamAPIKeys[i] = api.TeamAPIKey{
			Id:        apiKey.ID,
			Name:      apiKey.Name,
			KeyMask:   auth.MaskAPIKey(apiKey.APIKey),
			CreatedAt: apiKey.CreatedAt,
			CreatedBy: apiKey.CreatedBy,
			LastUsed:  apiKey.LastUsed,
		}
	}
	c.JSON(http.StatusOK, apiKeys)
}

func (a *APIStore) DeleteApikeysApiKeyID(c *gin.Context, apiKeyID string) {
	ctx := c.Request.Context()

	apiKeyIDParsed, err := uuid.Parse(apiKeyID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing API key ID: %s", err))

		errMsg := fmt.Errorf("error when parsing API key ID: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	err = a.db.Client.TeamAPIKey.DeleteOneID(apiKeyIDParsed).Exec(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting API key: %s", err))

		errMsg := fmt.Errorf("error when deleting API key: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	c.Status(http.StatusNoContent)
}

func (a *APIStore) PostApikeys(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)
	teamID := a.GetTeamInfo(c).Team.ID

	body, err := utils.ParseBody[api.NewTeamAPIKey](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	teamApiKey := auth.GenerateTeamAPIKey()
	apiKey, err := a.db.Client.TeamAPIKey.
		Create().
		SetTeamID(teamID).
		SetAPIKey(teamApiKey).
		SetCreatedBy(userID).
		SetLastUsed(time.Now()).
		SetUpdatedAt(time.Now()).
		SetAPIKeyMask(auth.MaskAPIKey(teamApiKey)).
		SetCreatedAt(time.Now()).
		SetName(body.Name).
		Save(ctx)

	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating API key: %s", err))

		errMsg := fmt.Errorf("error when creating API key: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
	}

	c.JSON(http.StatusCreated, api.CreatedTeamAPIKey{
		Id:        apiKey.ID,
		Key:       teamApiKey,
		KeyMask:   auth.MaskAPIKey(teamApiKey),
		Name:      apiKey.Name,
		CreatedBy: apiKey.CreatedBy,
		CreatedAt: apiKey.CreatedAt,
		LastUsed:  apiKey.LastUsed,
	})
}
