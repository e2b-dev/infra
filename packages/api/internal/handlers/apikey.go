package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
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
	if models.IsNotFound(err) {
		c.String(http.StatusNotFound, "id not found")
		return
	} else if err != nil {
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
		c.String(http.StatusInternalServerError, "Error when getting team API keys")

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

		keyValue := strings.Split(apiKey.APIKey, keys.ApiKeyPrefix)[1]

		// TODO: remove this once we migrate to hashed API keys
		KeyMask, err := keys.MaskKey(keys.ApiKeyPrefix, keyValue)
		if err != nil {
			fmt.Printf("masking API key failed %d: %v", apiKey.ID, err)
			continue
		}

		teamAPIKeys[i] = api.TeamAPIKey{
			Id:        apiKey.ID,
			Name:      apiKey.Name,
			KeyMask:   KeyMask,
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
	if models.IsNotFound(err) {
		c.String(http.StatusNotFound, "id not found")
		return
	} else if err != nil {
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

	apiKey, err := team.CreateAPIKey(ctx, a.db, teamID, userID, body.Name)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating team API key: %s", err))

		errMsg := fmt.Errorf("error when creating team API key: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	user, err := a.db.Client.User.Get(ctx, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		errMsg := fmt.Errorf("error when getting user: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	c.JSON(http.StatusCreated, api.CreatedTeamAPIKey{
		Id:      apiKey.ID,
		Key:     apiKey.APIKey,
		KeyMask: apiKey.APIKeyMask,
		Name:    apiKey.Name,
		CreatedBy: &api.TeamUser{
			Id:    user.ID,
			Email: user.Email,
		},
		CreatedAt: apiKey.CreatedAt,
		LastUsed:  apiKey.LastUsed,
	})
}
