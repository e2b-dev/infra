package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func Validate(ctx context.Context, sqlcDB *client.Client, token, envID string) (bool, error) {
	hashedToken, err := keys.VerifyKey(keys.AccessTokenPrefix, token)
	if err != nil {
		return false, err
	}

	_, err = sqlcDB.ValidateEnvBuilds(ctx, queries.ValidateEnvBuildsParams{
		TemplateID:      envID,
		AccessTokenHash: hashedToken,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func ValidateAccessToken(ctx context.Context, db *client.Client, accessToken string) bool {
	hashedToken, err := keys.VerifyKey(keys.AccessTokenPrefix, accessToken)
	if err != nil {
		return false
	}

	_, err = db.GetUserIDFromAccessToken(ctx, hashedToken)
	if err != nil {
		log.Printf("Error while checking access token: %s\n", err.Error())

		return false
	}

	return true
}

func ExtractAccessToken(authHeader, authType string) (string, error) {
	encodedLoginInfo := strings.TrimSpace(strings.TrimPrefix(authHeader, authType))

	loginInfo, err := base64.StdEncoding.DecodeString(encodedLoginInfo)
	if err != nil {
		return "", fmt.Errorf("error while decoding login info for %s: %w", encodedLoginInfo, err)
	}

	loginInfoParts := strings.Split(string(loginInfo), ":")
	if len(loginInfoParts) != 2 {
		return "", fmt.Errorf("invalid login info format %s", string(loginInfo))
	}

	username := loginInfoParts[0]
	if username != "_e2b_access_token" {
		return "", fmt.Errorf("invalid username %s", username)
	}

	accessToken := strings.TrimSpace(loginInfoParts[1])
	if strings.HasPrefix(accessToken, "\"") && strings.HasSuffix(accessToken, "\"") {
		return strings.Trim(accessToken, "\""), nil
	}
	// There can be extra whitespace in the token when the user uses Windows
	return accessToken, nil
}
