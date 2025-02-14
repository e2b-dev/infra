package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/accesstoken"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostAccesstokens(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	body, err := utils.ParseBody[api.NewAccessToken](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	accessToken, err := auth.GenerateAccessToken()
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when generating access token: %s", err))

		errMsg := fmt.Errorf("error when generating access token: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	accessTokenDB, err := a.db.Client.AccessToken.
		Create().
		SetUniqueID(uuid.New()).
		SetUserID(userID).
		SetID(accessToken).
		SetAccessTokenHash(auth.HashAccessToken(accessToken)).
		SetAccessTokenMask(auth.MaskAccessToken(accessToken)).
		SetCreatedAt(time.Now()).
		SetName(body.Name).
		Save(ctx)

	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating access token: %s", err))

		errMsg := fmt.Errorf("error when creating access token: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	c.JSON(http.StatusCreated, api.CreatedAccessToken{
		Id:        accessTokenDB.UniqueID,
		Token:     accessToken,
		TokenMask: accessTokenDB.AccessTokenMask,
		Name:      accessTokenDB.Name,
		CreatedAt: accessTokenDB.CreatedAt,
	})
}

func (a *APIStore) DeleteAccesstokensAccessTokenID(c *gin.Context, accessTokenID string) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	accessTokenIDParsed, err := uuid.Parse(accessTokenID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing access token ID: %s", err))

		errMsg := fmt.Errorf("error when parsing access token ID: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	_, err = a.db.Client.AccessToken.Delete().
		Where(accesstoken.UniqueIDEQ(accessTokenIDParsed), accesstoken.UserIDEQ(userID)).
		Exec(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting access token: %s", err))

		errMsg := fmt.Errorf("error when deleting access token: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	c.Status(http.StatusNoContent)
}
