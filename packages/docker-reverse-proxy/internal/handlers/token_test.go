package handlers

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func newTokenTestStore(t *testing.T, accessTokenAuthDisabled bool) (*APIStore, keys.Key) {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.DisableE2BAccessTokenAuthFlag.Key()).VariationForAll(accessTokenAuthDisabled))
	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	db := testutils.SetupDatabase(t)

	accessToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
	require.NoError(t, err)

	userID := uuid.New()
	require.NoError(t, db.AuthDB.Write.UpsertPublicUser(t.Context(), userID))

	_, err = db.AuthDB.Write.CreateAccessToken(t.Context(), authqueries.CreateAccessTokenParams{
		ID:                    uuid.New(),
		UserID:                userID,
		AccessTokenHash:       accessToken.HashedValue,
		AccessTokenPrefix:     accessToken.Masked.Prefix,
		AccessTokenLength:     int32(accessToken.Masked.ValueLength),
		AccessTokenMaskPrefix: accessToken.Masked.MaskedValuePrefix,
		AccessTokenMaskSuffix: accessToken.Masked.MaskedValueSuffix,
		Name:                  "Test token",
	})
	require.NoError(t, err)

	return &APIStore{
		db:           db.SqlcClient,
		authDb:       db.AuthDB,
		AuthCache:    cache.New(),
		featureFlags: ff,
	}, accessToken
}

func newTokenRequest(t *testing.T, rawAccessToken string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v2/token", nil)
	loginInfo := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "_e2b_access_token:%s", rawAccessToken))
	req.Header.Set("Authorization", "Basic "+loginInfo)

	return req
}

func TestGetTokenAcceptsAccessTokenWhenAuthEnabled(t *testing.T) {
	t.Parallel()

	store, accessToken := newTokenTestStore(t, false)

	recorder := httptest.NewRecorder()
	err := store.GetToken(recorder, newTokenRequest(t, accessToken.PrefixedRawValue))

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "token")
}

func TestGetTokenRejectsAccessTokenWhenAuthDisabled(t *testing.T) {
	t.Parallel()

	store, accessToken := newTokenTestStore(t, true)

	recorder := httptest.NewRecorder()
	err := store.GetToken(recorder, newTokenRequest(t, accessToken.PrefixedRawValue))

	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "E2B_API_KEY")
	require.Contains(t, recorder.Body.String(), "https://e2b.dev/docs/migration/access-token-deprecation")
}

func TestGetTokenRejectsInvalidAccessTokenRegardlessOfFlag(t *testing.T) {
	t.Parallel()

	store, _ := newTokenTestStore(t, true)

	recorder := httptest.NewRecorder()
	err := store.GetToken(recorder, newTokenRequest(t, keys.AccessTokenPrefix+"invalid"))

	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid access token")
}
