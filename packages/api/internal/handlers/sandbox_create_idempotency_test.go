package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/middleware/idempotency"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	sharedmiddleware "github.com/e2b-dev/infra/packages/shared/pkg/middleware"
	redisutils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestPostV2SandboxesSuppliesIdempotencyFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name            string
		projectDefault  *bool
		first           string
		retry           string
		wantRetryStatus int
	}{
		{
			name:  "ignored fields do not change the request",
			first: `{"templateID":"/"}`, retry: `{"templateID":"/","ignored":true}`,
			wantRetryStatus: http.StatusBadRequest,
		},
		{
			name:  "unconfigured project retains the legacy public fingerprint",
			first: `{"templateID":"/","network":{"allowPublicTraffic":true}}`,
			retry: `{"templateID":"/","network":{}}`, wantRetryStatus: http.StatusBadRequest,
		},
		{
			name: "configured private project distinguishes omission", projectDefault: new(false),
			first: `{"templateID":"/","network":{"allowPublicTraffic":true}}`,
			retry: `{"templateID":"/","network":{}}`, wantRetryStatus: http.StatusConflict,
		},
		{
			name: "configured public project distinguishes omission", projectDefault: new(true),
			first: `{"templateID":"/","network":{"allowPublicTraffic":true}}`,
			retry: `{"templateID":"/","network":{}}`, wantRetryStatus: http.StatusConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := redisutils.SetupContainerInstance(t)
			source := ldtestdata.DataSource()
			source.Update(source.Flag(featureflags.APIIdempotencyRoutes.Key()).ValueForAll(ldvalue.CopyArbitraryValue(map[string]bool{"POST /v2/sandboxes": true})))
			flags, err := featureflags.NewClientWithDatasource(source)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, flags.Close(context.WithoutCancel(t.Context()))) })
			team := &types.Team{
				Team: &authqueries.Team{ID: uuid.New()},
				ProjectSettings: dbtypes.ProjectSettings{
					SandboxSettings: dbtypes.SandboxSettings{DefaultAllowPublicTraffic: test.projectDefault},
				},
			}
			r := gin.New()
			r.Use(func(c *gin.Context) { auth.SetTeamInfoForTest(t, c, team) })
			r.Use(sharedmiddleware.RequestTimeout(time.Minute), idempotency.Middleware(client, flags))
			store := &APIStore{}
			r.POST("/v2/sandboxes", func(c *gin.Context) {
				store.PostV2Sandboxes(c, api.PostV2SandboxesParams{})
			})

			var original string
			for i, body := range []string{test.first, test.retry} {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v2/sandboxes", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "samerequest")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if i == 0 {
					require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
					require.Contains(t, w.Body.String(), "Invalid template reference")
					original = w.Body.String()
				} else {
					require.Equal(t, test.wantRetryStatus, w.Code, w.Body.String())
					if test.wantRetryStatus == http.StatusBadRequest {
						require.Equal(t, original, w.Body.String())
					} else {
						require.Contains(t, w.Body.String(), "idempotency_request_mismatch")
					}
				}
			}
			keys, _, err := client.Scan(t.Context(), 0, "api:idempotency:"+team.ID.String()+":*", 10).Result()
			require.NoError(t, err)
			require.Len(t, keys, 1)
			require.True(t, client.HExists(t.Context(), keys[0], "response").Val())
		})
	}
}
