package handlers

import (
	"fmt"
	"net/http"
	"strings"

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

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	accessToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when generating access token: %s", err))

		errMsg := fmt.Errorf("error when generating access token: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	accessTokenDB, err := a.db.Client.AccessToken.
		Create().
		SetID(uuid.New()).
		SetUserID(userID).
		SetAccessToken(accessToken.PrefixedRawValue).
		SetAccessTokenHash(accessToken.HashedValue).
		SetAccessTokenMask(accessToken.MaskedValue).
		SetName(body.Name).
		Save(ctx)

	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating access token: %s", err))

		errMsg := fmt.Errorf("error when creating access token: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	valueWithoutPrefix := strings.TrimPrefix(accessToken.PrefixedRawValue, keys.AccessTokenPrefix)

	maskedToken, err := keys.GetMaskedIdentifierProperties(keys.AccessTokenPrefix, valueWithoutPrefix)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when masking access token: %s", err))

		errMsg := fmt.Errorf("error when masking access token: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	c.JSON(http.StatusCreated, api.CreatedAccessToken{
		Id:    accessTokenDB.ID,
		Token: accessToken.PrefixedRawValue,
		Masking: api.IdentifierMaskingDetails{
			Prefix:            maskedToken.Prefix,
			ValueLength:       maskedToken.ValueLength,
			MaskedValuePrefix: maskedToken.MaskedValuePrefix,
			MaskedValueSuffix: maskedToken.MaskedValueSuffix,
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

		errMsg := fmt.Errorf("error when parsing access token ID: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
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

		errMsg := fmt.Errorf("error when deleting access token: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	c.Status(http.StatusNoContent)
}
