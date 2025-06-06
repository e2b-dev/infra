package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/accesstoken"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostAccessTokens(c *gin.Context) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	body, err := utils.ParseBody[api.NewAccessToken](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	accessToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when generating access token: %s", err))

		telemetry.ReportCriticalError(ctx, "error when generating access token", err)

		return
	}

	accessTokenDB, err := a.db.Client.AccessToken.
		Create().
		SetID(uuid.New()).
		SetUserID(userID).
		SetAccessToken(accessToken.PrefixedRawValue).
		SetAccessTokenHash(accessToken.HashedValue).
		SetAccessTokenPrefix(accessToken.Masked.Prefix).
		SetAccessTokenLength(accessToken.Masked.ValueLength).
		SetAccessTokenMaskPrefix(accessToken.Masked.MaskedValuePrefix).
		SetAccessTokenMaskSuffix(accessToken.Masked.MaskedValueSuffix).
		SetName(body.Name).
		Save(ctx)

	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating access token: %s", err))

		telemetry.ReportCriticalError(ctx, "error when creating access token", err)

		return
	}

	c.JSON(http.StatusCreated, api.CreatedAccessToken{
		Id:    accessTokenDB.ID,
		Token: accessToken.PrefixedRawValue,
		Mask: api.IdentifierMaskingDetails{
			Prefix:            accessTokenDB.AccessTokenPrefix,
			ValueLength:       accessTokenDB.AccessTokenLength,
			MaskedValuePrefix: accessTokenDB.AccessTokenMaskPrefix,
			MaskedValueSuffix: accessTokenDB.AccessTokenMaskSuffix,
		},
		Name:      accessTokenDB.Name,
		CreatedAt: accessTokenDB.CreatedAt,
	})
}

func (a *APIStore) DeleteAccessTokensAccessTokenID(c *gin.Context, accessTokenID string) {
	ctx := c.Request.Context()

	userID := a.GetUserID(c)

	accessTokenIDParsed, err := uuid.Parse(accessTokenID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing access token ID: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing access token ID", err)

		return
	}

	n, err := a.db.Client.AccessToken.Delete().
		Where(accesstoken.IDEQ(accessTokenIDParsed), accesstoken.UserIDEQ(userID)).
		Exec(ctx)
	if n < 1 {
		c.String(http.StatusNotFound, "id not found")
		return
	} else if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting access token: %s", err))

		telemetry.ReportCriticalError(ctx, "error when deleting access token", err)

		return
	}

	c.Status(http.StatusNoContent)
}
