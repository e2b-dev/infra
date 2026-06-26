package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func TestPostAccessTokensRejectsWhenIssuanceDisabled(t *testing.T) {
	t.Parallel()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.DisableE2BAccessTokenProvisioningFlag.Key()).VariationForAll(true))
	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/access-tokens", strings.NewReader(`{"name":"my token"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	sharedauth.SetUserIDForTest(t, ginCtx, uuid.New())

	store := &APIStore{featureFlags: ff}
	store.PostAccessTokens(ginCtx)

	require.Equal(t, http.StatusGone, recorder.Code)
	require.Contains(t, recorder.Body.String(), "E2B_API_KEY")
}
