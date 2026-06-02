package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestGetTemplatesTemplateIDTagsGroups_PaginatesAcrossPages(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	expectedTags := make([]string, 0, 30)
	for i := range 30 {
		tag := fmt.Sprintf("tag-%02d", i)
		expectedTags = append(expectedTags, tag)
		buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
		insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, tag, baseTime.Add(time.Duration(i)*time.Minute))
	}

	tagsLimit := api.TagGroupsLimit(10)
	seen := make(map[string]struct{})
	var cursor *api.TagGroupsCursor

	for page := range 5 {
		response := callTagGroupsHandlerFull(t, ctx, testDB, teamID, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{
			TagsLimit:  &tagsLimit,
			TagsCursor: cursor,
		})

		for _, group := range response.Tags {
			_, dup := seen[group.Tag]
			require.False(t, dup, "page %d returned duplicate tag %q", page, group.Tag)
			seen[group.Tag] = struct{}{}
		}

		if response.NextCursor == nil || *response.NextCursor == "" {
			break
		}
		cursor = response.NextCursor
	}

	assert.Len(t, seen, len(expectedTags), "expected to walk every tag exactly once")
}

func TestGetTemplatesTemplateIDTagsGroups_StableKeysetOnSameLatest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	sameTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	tags := []string{"alpha", "beta", "gamma", "delta"}
	for _, tag := range tags {
		buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
		insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, tag, sameTime)
	}

	tagsLimit := api.TagGroupsLimit(2)
	first := callTagGroupsHandlerFull(t, ctx, testDB, teamID, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{
		TagsLimit: &tagsLimit,
	})
	require.Len(t, first.Tags, 2)
	require.NotNil(t, first.NextCursor)

	second := callTagGroupsHandlerFull(t, ctx, testDB, teamID, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{
		TagsLimit:  &tagsLimit,
		TagsCursor: first.NextCursor,
	})
	require.Len(t, second.Tags, 2)

	got := []string{first.Tags[0].Tag, first.Tags[1].Tag, second.Tags[0].Tag, second.Tags[1].Tag}
	assert.Equal(t, []string{"alpha", "beta", "delta", "gamma"}, got, "alphabetical tiebreak on equal latest_assigned_at")
}

func TestGetTemplatesTemplateIDTagsGroups_SortVariants(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	// Insert so each tag has a unique latest_assigned_at and a non-alphabetical order:
	//   prod   → newest
	//   alpha  → middle
	//   beta   → oldest
	tagsByAge := []string{"beta", "alpha", "prod"} // oldest -> newest
	for i, tag := range tagsByAge {
		buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
		insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, tag, baseTime.Add(time.Duration(i)*time.Hour))
	}

	cases := []struct {
		name string
		sort api.GetTemplatesTemplateIDTagsGroupsParamsSort
		want []string
	}{
		{"latest_desc", api.GetTemplatesTemplateIDTagsGroupsParamsSort(tagGroupsSortLatestDesc), []string{"prod", "alpha", "beta"}},
		{"latest_asc", api.GetTemplatesTemplateIDTagsGroupsParamsSort(tagGroupsSortLatestAsc), []string{"beta", "alpha", "prod"}},
		{"name_asc", api.GetTemplatesTemplateIDTagsGroupsParamsSort(tagGroupsSortNameAsc), []string{"alpha", "beta", "prod"}},
		{"name_desc", api.GetTemplatesTemplateIDTagsGroupsParamsSort(tagGroupsSortNameDesc), []string{"prod", "beta", "alpha"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sortParam := tc.sort
			response := callTagGroupsHandlerFull(t, ctx, testDB, teamID, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{
				Sort: &sortParam,
			})
			got := make([]string, len(response.Tags))
			for i, g := range response.Tags {
				got[i] = g.Tag
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestGetTemplatesTemplateIDTagsGroups_Search(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	all := []string{"prod.v1.0", "prod.v1x0", "a_b", "axb", "staging"}
	for i, tag := range all {
		buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
		insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, tag, baseTime.Add(time.Duration(i)*time.Hour))
	}

	cases := []struct {
		name   string
		search string
		want   []string
	}{
		{"prefix prod", "prod", []string{"prod.v1.0", "prod.v1x0"}},
		{"dot is literal", "v1.0", []string{"prod.v1.0"}},
		{"underscore is literal not wildcard", "a_b", []string{"a_b"}},
		{"no match", "missing", []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			searchParam := tc.search
			response := callTagGroupsHandlerFull(t, ctx, testDB, teamID, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{
				Search: &searchParam,
			})
			got := make([]string, 0, len(response.Tags))
			for _, g := range response.Tags {
				got = append(got, g.Tag)
			}
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestGetTemplatesTemplateIDTagsGroups_InvalidInputs(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	cases := []struct {
		name   string
		params api.GetTemplatesTemplateIDTagsGroupsParams
	}{
		{
			name:   "search contains percent",
			params: api.GetTemplatesTemplateIDTagsGroupsParams{Search: new("%")},
		},
		{
			name:   "search contains at sign",
			params: api.GetTemplatesTemplateIDTagsGroupsParams{Search: new("a@b")},
		},
		{
			name:   "sort is bogus",
			params: api.GetTemplatesTemplateIDTagsGroupsParams{Sort: new(api.GetTemplatesTemplateIDTagsGroupsParamsSort("bogus"))},
		},
		{
			name:   "cursor malformed",
			params: api.GetTemplatesTemplateIDTagsGroupsParams{TagsCursor: new("malformed")},
		},
		{
			name: "cursor sort mismatch",
			params: api.GetTemplatesTemplateIDTagsGroupsParams{
				Sort:       new(api.GetTemplatesTemplateIDTagsGroupsParamsSort(tagGroupsSortLatestDesc)),
				TagsCursor: new("name_asc|2026-01-01T00:00:00Z|x"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder, ginCtx := newTemplateTagsTestContext(t, ctx, teamID)
			store := &APIStore{db: testDB.SqlcClient}
			//nolint:contextcheck // GetTemplatesTemplateIDTagsGroups reads ctx from ginCtx.Request.Context().
			store.GetTemplatesTemplateIDTagsGroups(ginCtx, templateID, tc.params)
			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
}

func TestGetTemplatesTemplateIDTagsGroups_NextCursorAbsentOnLastPage(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	for i := range 3 {
		buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
		insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, fmt.Sprintf("tag-%d", i), baseTime.Add(time.Duration(i)*time.Hour))
	}

	tagsLimit := api.TagGroupsLimit(3)
	response := callTagGroupsHandlerFull(t, ctx, testDB, teamID, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{
		TagsLimit: &tagsLimit,
	})

	require.Len(t, response.Tags, 3)
	assert.Nil(t, response.NextCursor)
}

func TestGetTemplatesTemplateIDTagsCount_ReturnsDistinctReadyTagCount(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	for i := range 5 {
		tag := fmt.Sprintf("tag-%d", i)
		// Two ready builds with the same tag should be counted once.
		for range 2 {
			buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
			insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, tag, baseTime.Add(time.Duration(i)*time.Hour))
		}
	}

	response := callTagCountHandler(t, ctx, testDB, teamID, templateID)
	assert.Equal(t, int64(5), response.Total)
}

func TestGetTemplatesTemplateIDTagsCount_OnlyReadyBuildsCounted(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	readyBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
	failedBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "failed")
	waitingBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "waiting")
	insertTemplateTagAssignment(t, ctx, testDB, templateID, readyBuildID, "prod", baseTime)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, failedBuildID, "dev", baseTime)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, waitingBuildID, "staging", baseTime)

	response := callTagCountHandler(t, ctx, testDB, teamID, templateID)
	assert.Equal(t, int64(1), response.Total)
}

func TestGetTemplatesTemplateIDTagsCount_EmptyTemplate(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	response := callTagCountHandler(t, ctx, testDB, teamID, templateID)
	assert.Equal(t, int64(0), response.Total)
}

func TestGetTemplatesTemplateIDTagsCount_NotFoundForOtherTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	ownerTeamID := testutils.CreateTestTeam(t, testDB)
	otherTeamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, ownerTeamID)

	recorder, ginCtx := newTemplateTagsTestContext(t, ctx, otherTeamID)
	store := &APIStore{db: testDB.SqlcClient}
	store.GetTemplatesTemplateIDTagsCount(ginCtx, templateID)

	assert.Equal(t, http.StatusNotFound, recorder.Code,
		"team mismatch should not leak template existence")
}

func TestParseTagGroupsCursor_FormatsAndParses(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	tag := "prod-v1"
	cursor := formatTagGroupsCursor(tagGroupsSortLatestDesc, ts, tag)
	require.True(t, strings.HasPrefix(cursor, "latest_desc|"))

	cursorTime, cursorTag, err := parseTagGroupsCursor(&cursor, tagGroupsSortLatestDesc)
	require.NoError(t, err)
	require.NotNil(t, cursorTime)
	require.NotNil(t, cursorTag)
	assert.True(t, cursorTime.Equal(ts))
	assert.Equal(t, tag, *cursorTag)
}

func TestParseTagGroupsCursor_FirstPageReturnsNilPointers(t *testing.T) {
	t.Parallel()

	for _, sort := range []tagGroupsSort{
		tagGroupsSortLatestDesc, tagGroupsSortLatestAsc,
		tagGroupsSortNameAsc, tagGroupsSortNameDesc,
	} {
		cursorTime, cursorTag, err := parseTagGroupsCursor(nil, sort)
		require.NoError(t, err)
		assert.Nil(t, cursorTime, sort)
		assert.Nil(t, cursorTag, sort)
	}
}

func callTagGroupsHandlerFull(
	t *testing.T,
	ctx context.Context,
	testDB *testutils.Database,
	teamID uuid.UUID,
	templateID string,
	params api.GetTemplatesTemplateIDTagsGroupsParams,
) api.TemplateTagGroupsResponse {
	t.Helper()

	recorder, ginCtx := newTemplateTagsTestContextWithMethod(t, ctx, teamID, http.MethodGet)
	store := &APIStore{db: testDB.SqlcClient}
	//nolint:contextcheck // GetTemplatesTemplateIDTagsGroups reads ctx from ginCtx.Request.Context().
	store.GetTemplatesTemplateIDTagsGroups(ginCtx, templateID, params)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response api.TemplateTagGroupsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	return response
}

func callTagCountHandler(
	t *testing.T,
	ctx context.Context,
	testDB *testutils.Database,
	teamID uuid.UUID,
	templateID string,
) api.TemplateTagsCountResponse {
	t.Helper()

	recorder, ginCtx := newTemplateTagsTestContext(t, ctx, teamID)
	store := &APIStore{db: testDB.SqlcClient}
	//nolint:contextcheck // GetTemplatesTemplateIDTagsCount reads ctx from ginCtx.Request.Context().
	store.GetTemplatesTemplateIDTagsCount(ginCtx, templateID)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response api.TemplateTagsCountResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	return response
}

func newTemplateTagsTestContextWithMethod(t *testing.T, ctx context.Context, teamID uuid.UUID, method string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, method, "/", nil)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	return recorder, ginCtx
}
