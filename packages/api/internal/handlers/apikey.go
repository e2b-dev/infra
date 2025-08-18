package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/teamapikey"
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

	apiKey, err := a.db.Client.TeamAPIKey.Query().Where(teamapikey.ID(apiKeyIDParsed), teamapikey.TeamID(teamID)).First(ctx)
	if err != nil {
		if models.IsNotFound(err) {
			c.String(http.StatusNotFound, "id not found")
			return
		}
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when querying team API key: %s", err))

		telemetry.ReportCriticalError(ctx, "error when querying team API key", err)
		return
	}

	err = a.db.Client.TeamAPIKey.UpdateOneID(apiKeyIDParsed).Where(teamapikey.TeamID(teamID)).SetName(body.Name).SetUpdatedAt(time.Now()).Exec(ctx)
	if models.IsNotFound(err) {
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

	apiKeysDB, err := a.db.Client.TeamAPIKey.
		Query().
		Where(teamapikey.TeamID(teamID)).
		WithCreator().
		All(ctx)
	if err != nil {
		zap.L().Warn("error when getting team API keys", zap.Error(err))
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
		maskedKeyProperties, err := keys.MaskKey(keys.ApiKeyPrefix, keyValue)
		if err != nil {
			fmt.Printf("masking API key failed %d: %v", apiKey.ID, err)
			continue
		}

		teamAPIKeys[i] = api.TeamAPIKey{
			Id:   apiKey.ID,
			Name: apiKey.Name,
			Mask: api.IdentifierMaskingDetails{
				Prefix:            maskedKeyProperties.Prefix,
				ValueLength:       maskedKeyProperties.ValueLength,
				MaskedValuePrefix: maskedKeyProperties.MaskedValuePrefix,
				MaskedValueSuffix: maskedKeyProperties.MaskedValueSuffix,
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

	err = a.db.Client.TeamAPIKey.DeleteOneID(apiKeyIDParsed).Where(teamapikey.TeamID(teamID)).Exec(ctx)
	if models.IsNotFound(err) {
		c.String(http.StatusNotFound, "id not found")
		return
	} else if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting API key: %s", err))

		telemetry.ReportCriticalError(ctx, "error when deleting API key", err)
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

	apiKey, err := team.CreateAPIKey(ctx, a.db, teamID, userID, body.Name)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating team API key: %s", err))

		telemetry.ReportCriticalError(ctx, "error when creating team API key", err)

		return
	}

	user, err := a.db.Client.User.Get(ctx, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return
	}

	c.JSON(http.StatusCreated, api.CreatedTeamAPIKey{
		Id:   apiKey.ID,
		Name: apiKey.Name,
		Key:  apiKey.APIKey,
		Mask: api.IdentifierMaskingDetails{
			Prefix:            apiKey.APIKeyPrefix,
			ValueLength:       apiKey.APIKeyLength,
			MaskedValuePrefix: apiKey.APIKeyMaskPrefix,
			MaskedValueSuffix: apiKey.APIKeyMaskSuffix,
		},
		CreatedBy: &api.TeamUser{
			Id:    user.ID,
			Email: user.Email,
		},
		CreatedAt: apiKey.CreatedAt,
		LastUsed:  apiKey.LastUsed,
	})
}
