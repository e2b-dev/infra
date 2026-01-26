package builds

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testutils2 "github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func TestInvalidateUnstartedTemplateBuilds_InvalidatesWaitingBuilds(t *testing.T) {
	t.Parallel()
	db := testutils2.SetupDatabase(t)
	ctx := t.Context()

	// Create template with a waiting build
	teamID := testutils2.CreateTestTeam(t, db)
	templateID := testutils2.CreateTestTemplate(t, db, teamID)
	buildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	// Invalidate waiting builds for default tag
	err := db.SqlcClient.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tags:       []string{"default"},
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify build status changed to failed
	status := testutils2.GetBuildStatus(t, ctx, db, buildID)
	assert.Equal(t, "failed", status, "Waiting build should be invalidated to failed")
}

func TestInvalidateUnstartedTemplateBuilds_OnlyAffectsSpecificTag(t *testing.T) {
	t.Parallel()
	db := testutils2.SetupDatabase(t)
	ctx := t.Context()

	// Create template with builds assigned to different tags
	teamID := testutils2.CreateTestTeam(t, db)
	templateID := testutils2.CreateTestTemplate(t, db, teamID)

	// Build with 'default' tag
	defaultBuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, defaultBuildID, "default")

	// Build with 'v1' tag only (delete trigger-created 'default' assignment)
	v1BuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.DeleteTriggerBuildAssignment(t, ctx, db, templateID, v1BuildID, "default")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, v1BuildID, "v1")

	// Invalidate only 'default' tag builds
	err := db.SqlcClient.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tags:       []string{"default"},
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify only default build was invalidated
	defaultStatus := testutils2.GetBuildStatus(t, ctx, db, defaultBuildID)
	assert.Equal(t, "failed", defaultStatus, "Default tag build should be invalidated")

	v1Status := testutils2.GetBuildStatus(t, ctx, db, v1BuildID)
	assert.Equal(t, "waiting", v1Status, "v1 tag build should NOT be affected")
}

func TestInvalidateUnstartedTemplateBuilds_DoesNotAffectOtherTemplates(t *testing.T) {
	t.Parallel()
	db := testutils2.SetupDatabase(t)
	ctx := t.Context()

	// Create two templates with waiting builds
	teamID := testutils2.CreateTestTeam(t, db)
	template1ID := testutils2.CreateTestTemplate(t, db, teamID)
	template2ID := testutils2.CreateTestTemplate(t, db, teamID)

	build1ID := testutils2.CreateTestBuild(t, ctx, db, template1ID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, template1ID, build1ID, "default")

	build2ID := testutils2.CreateTestBuild(t, ctx, db, template2ID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, template2ID, build2ID, "default")

	// Invalidate only template1's builds
	err := db.SqlcClient.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: template1ID,
		Tags:       []string{"default"},
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify only template1's build was invalidated
	status1 := testutils2.GetBuildStatus(t, ctx, db, build1ID)
	assert.Equal(t, "failed", status1, "Template1's build should be invalidated")

	status2 := testutils2.GetBuildStatus(t, ctx, db, build2ID)
	assert.Equal(t, "waiting", status2, "Template2's build should NOT be affected")
}

func TestInvalidateUnstartedTemplateBuilds_DoesNotAffectNonWaitingBuilds(t *testing.T) {
	t.Parallel()
	db := testutils2.SetupDatabase(t)
	ctx := t.Context()

	// Create template with builds in various states
	teamID := testutils2.CreateTestTeam(t, db)
	templateID := testutils2.CreateTestTemplate(t, db, teamID)

	waitingBuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, waitingBuildID, "default")

	buildingBuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "building")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, buildingBuildID, "default")

	uploadedBuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "uploaded")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, uploadedBuildID, "default")

	// Invalidate waiting builds
	err := db.SqlcClient.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tags:       []string{"default"},
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify only waiting build was invalidated
	waitingStatus := testutils2.GetBuildStatus(t, ctx, db, waitingBuildID)
	assert.Equal(t, "failed", waitingStatus, "Waiting build should be invalidated")

	buildingStatus := testutils2.GetBuildStatus(t, ctx, db, buildingBuildID)
	assert.Equal(t, "building", buildingStatus, "Building build should NOT be affected")

	uploadedStatus := testutils2.GetBuildStatus(t, ctx, db, uploadedBuildID)
	assert.Equal(t, "uploaded", uploadedStatus, "Uploaded build should NOT be affected")
}

func TestInvalidateUnstartedTemplateBuilds_MultipleWaitingBuilds(t *testing.T) {
	t.Parallel()
	db := testutils2.SetupDatabase(t)
	ctx := t.Context()

	// Create template with multiple waiting builds
	teamID := testutils2.CreateTestTeam(t, db)
	templateID := testutils2.CreateTestTemplate(t, db, teamID)

	build1ID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, build1ID, "default")

	build2ID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, build2ID, "default")

	build3ID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, build3ID, "default")

	// Invalidate all waiting builds
	err := db.SqlcClient.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tags:       []string{"default"},
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify all waiting builds were invalidated
	assert.Equal(t, "failed", testutils2.GetBuildStatus(t, ctx, db, build1ID), "Build 1 should be invalidated")
	assert.Equal(t, "failed", testutils2.GetBuildStatus(t, ctx, db, build2ID), "Build 2 should be invalidated")
	assert.Equal(t, "failed", testutils2.GetBuildStatus(t, ctx, db, build3ID), "Build 3 should be invalidated")
}

func TestInvalidateUnstartedTemplateBuilds_MultipleTagsInSingleCall(t *testing.T) {
	t.Parallel()
	db := testutils2.SetupDatabase(t)
	ctx := t.Context()

	// Create template with builds assigned to different tags
	teamID := testutils2.CreateTestTeam(t, db)
	templateID := testutils2.CreateTestTemplate(t, db, teamID)

	// Build with 'default' tag
	defaultBuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, defaultBuildID, "default")

	// Build with 'v1' tag only (delete trigger-created 'default' assignment)
	v1BuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.DeleteTriggerBuildAssignment(t, ctx, db, templateID, v1BuildID, "default")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, v1BuildID, "v1")

	// Build with 'v2' tag only (delete trigger-created 'default' assignment)
	v2BuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.DeleteTriggerBuildAssignment(t, ctx, db, templateID, v2BuildID, "default")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, v2BuildID, "v2")

	// Build with 'stable' tag only (should NOT be affected) - delete trigger-created 'default' assignment
	stableBuildID := testutils2.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils2.DeleteTriggerBuildAssignment(t, ctx, db, templateID, stableBuildID, "default")
	testutils2.CreateTestBuildAssignment(t, ctx, db, templateID, stableBuildID, "stable")

	// Invalidate 'default', 'v1', and 'v2' tags in a single call
	err := db.SqlcClient.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tags:       []string{"default", "v1", "v2"},
		Reason:     types.BuildReason{Message: "Test batch invalidation"},
	})
	require.NoError(t, err)

	// Verify the specified tags were invalidated
	assert.Equal(t, "failed", testutils2.GetBuildStatus(t, ctx, db, defaultBuildID), "Default tag build should be invalidated")
	assert.Equal(t, "failed", testutils2.GetBuildStatus(t, ctx, db, v1BuildID), "v1 tag build should be invalidated")
	assert.Equal(t, "failed", testutils2.GetBuildStatus(t, ctx, db, v2BuildID), "v2 tag build should be invalidated")

	// Verify the 'stable' tag was NOT affected
	assert.Equal(t, "waiting", testutils2.GetBuildStatus(t, ctx, db, stableBuildID), "stable tag build should NOT be affected")
}
