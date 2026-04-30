package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostAccessTokens(c *gin.Context) {
	ctx := c.Request.Context()

	userID := auth.MustGetUserID(c)

	body, err := ginutils.ParseBody[api.NewAccessToken](ctx, c)
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

	accessTokenDB, err := a.authDB.Write.CreateAccessToken(ctx, authqueries.CreateAccessTokenParams{
		ID:                    uuid.New(),
		UserID:                userID,
		AccessTokenHash:       accessToken.HashedValue,
		AccessTokenPrefix:     accessToken.Masked.Prefix,
		AccessTokenLength:     int32(accessToken.Masked.ValueLength),
		AccessTokenMaskPrefix: accessToken.Masked.MaskedValuePrefix,
		AccessTokenMaskSuffix: accessToken.Masked.MaskedValueSuffix,
		Name:                  body.Name,
	})
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
			ValueLength:       int(accessTokenDB.AccessTokenLength),
			MaskedValuePrefix: accessTokenDB.AccessTokenMaskPrefix,
			MaskedValueSuffix: accessTokenDB.AccessTokenMaskSuffix,
		},
		Name:      accessTokenDB.Name,
		CreatedAt: accessTokenDB.CreatedAt,
	})
}

func (a *APIStore) DeleteAccessTokensAccessTokenID(c *gin.Context, accessTokenID string) {
	ctx := c.Request.Context()

	userID := auth.MustGetUserID(c)

	accessTokenIDParsed, err := uuid.Parse(accessTokenID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing access token ID: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing access token ID", err)

		return
	}

	_, err = a.authDB.Write.DeleteAccessToken(ctx, authqueries.DeleteAccessTokenParams{
		ID:     accessTokenIDParsed,
		UserID: userID,
	})
	if dberrors.IsNotFoundError(err) {
		c.String(http.StatusNotFound, "id not found")

		return
	} else if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting access token: %s", err))

		telemetry.ReportCriticalError(ctx, "error when deleting access token", err)

		return
	}

	c.Status(http.StatusNoContent)
}
