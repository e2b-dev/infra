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
	"github.com/e2b-dev/infra/packages/shared/pkg/models/teamapikey"
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

	apiKeysDB, err := a.db.Client.TeamAPIKey.
		Query().
		Where(teamapikey.TeamID(teamID)).
		WithCreator().
		All(ctx)
	if err != nil {
		log.Println("Error when getting team API keys: ", err)
		c.JSON(http.StatusInternalServerError, "Error when getting team API keys")

		return
	}

	teamAPIKeys := make([]api.TeamAPIKey, len(apiKeysDB))
	for i, apiKey := range apiKeysDB {
		var createdBy *api.TeamUser
		if apiKey.Edges.Creator != nil {
			createdBy = &api.TeamUser{
				Email: apiKey.Edges.Creator.Email,
				Id:    apiKey.Edges.Creator.ID,
			}
		}

		teamAPIKeys[i] = api.TeamAPIKey{
			Id:        apiKey.ID,
			Name:      apiKey.Name,
			KeyMask:   auth.MaskAPIKey(apiKey.APIKey),
			CreatedAt: apiKey.CreatedAt,
			CreatedBy: createdBy,
			LastUsed:  apiKey.LastUsed,
		}
	}
	c.JSON(http.StatusOK, teamAPIKeys)
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
		SetCreatedBy(userID).
		SetLastUsed(time.Now()).
		SetUpdatedAt(time.Now()).
		SetAPIKey(teamApiKey).
		SetAPIKeyMask(auth.MaskAPIKey(teamApiKey)).
		SetAPIKeyHash(auth.HashAPIKey(teamApiKey)).
		SetCreatedAt(time.Now()).
		SetName(body.Name).
		Save(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating API key: %s", err))

		errMsg := fmt.Errorf("error when creating API key: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
	}

	user, err := a.db.Client.User.Get(ctx, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		errMsg := fmt.Errorf("error when getting user: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
	}

	c.JSON(http.StatusCreated, api.CreatedTeamAPIKey{
		Id:      apiKey.ID,
		Key:     teamApiKey,
		KeyMask: auth.MaskAPIKey(teamApiKey),
		Name:    apiKey.Name,
		CreatedBy: &api.TeamUser{
			Id:    user.ID,
			Email: user.Email,
		},
		CreatedAt: apiKey.CreatedAt,
		LastUsed:  apiKey.LastUsed,
	})
}
