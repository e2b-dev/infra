package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestPostSandboxes_MissingTagDisclosure(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	ownerTeamSlug := testutils.GetTeamSlug(t, ctx, db, ownerTeamID)
	requesterTeamID := testutils.CreateTestTeam(t, db)
	requesterTeamSlug := testutils.GetTeamSlug(t, ctx, db, requesterTeamID)

	store := &APIStore{
		templateCache: templatecache.NewTemplateCache(db.SqlcClient, redis),
	}
	defer func() {
		require.NoError(t, store.templateCache.Close(ctx))
	}()

	tests := []struct {
		name   string
		public bool
		alias  string
	}{
		{
			name:   "public template returns tag-specific not found",
			public: true,
			alias:  "public-missing-tag",
		},
		{
			name:   "private template stays generic not found",
			public: false,
			alias:  "private-missing-tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templateID := testutils.CreateTestTemplate(t, db, ownerTeamID)
			setTemplatePublic(t, db, templateID, tt.public)
			testutils.CreateTestTemplateAliasWithName(t, db, templateID, tt.alias, &ownerTeamSlug)

			buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
			testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, id.DefaultTag)

			templateRef := id.WithTag(id.WithNamespace(ownerTeamSlug, tt.alias), "v2")
			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)

			body, err := json.Marshal(api.PostSandboxesJSONRequestBody{TemplateID: templateRef})
			require.NoError(t, err)

			ginCtx.Request = httptest.NewRequest(http.MethodPost, "/sandboxes", bytes.NewReader(body))
			ginCtx.Request.Header.Set("Content-Type", "application/json")
			auth.SetTeamInfo(ginCtx, &authtypes.Team{
				Team: &authqueries.Team{
					ID:   requesterTeamID,
					Slug: requesterTeamSlug,
				},
				Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
			})

			store.PostSandboxes(ginCtx)

			require.Equal(t, http.StatusNotFound, recorder.Code)

			var apiErr api.Error
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &apiErr))

			wantMessage := fmt.Sprintf("template '%s' (%s) not found", id.WithNamespace(ownerTeamSlug, tt.alias), templateID)
			if tt.public {
				wantMessage = fmt.Sprintf("template '%s' (%s) with tag 'v2' not found", id.WithNamespace(ownerTeamSlug, tt.alias), templateID)
			}

			assert.Equal(t, int32(http.StatusNotFound), apiErr.Code)
			assert.Equal(t, wantMessage, apiErr.Message)
		})
	}
}

func setTemplatePublic(t *testing.T, db *testutils.Database, templateID string, public bool) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(
		t.Context(),
		"UPDATE public.envs SET public = $2 WHERE id = $1",
		templateID,
		public,
	)
	require.NoError(t, err)
}
