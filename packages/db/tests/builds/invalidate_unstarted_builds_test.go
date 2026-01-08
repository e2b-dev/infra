package builds

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/testutils"
	"github.com/e2b-dev/infra/packages/db/types"
)

func TestInvalidateUnstartedTemplateBuilds_InvalidatesWaitingBuilds(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create template with a waiting build
	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	// Invalidate waiting builds for default tag
	err := db.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tag:        "default",
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify build status changed to failed
	status := testutils.GetBuildStatus(t, ctx, db, buildID)
	assert.Equal(t, "failed", status, "Waiting build should be invalidated to failed")
}

func TestInvalidateUnstartedTemplateBuilds_OnlyAffectsSpecificTag(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create template with builds assigned to different tags
	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	// Build with 'default' tag
	defaultBuildID := testutils.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, defaultBuildID, "default")

	// Build with 'v1' tag
	v1BuildID := testutils.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, v1BuildID, "v1")

	// Invalidate only 'default' tag builds
	err := db.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tag:        "default",
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify only default build was invalidated
	defaultStatus := testutils.GetBuildStatus(t, ctx, db, defaultBuildID)
	assert.Equal(t, "failed", defaultStatus, "Default tag build should be invalidated")

	v1Status := testutils.GetBuildStatus(t, ctx, db, v1BuildID)
	assert.Equal(t, "waiting", v1Status, "v1 tag build should NOT be affected")
}

func TestInvalidateUnstartedTemplateBuilds_DoesNotAffectOtherTemplates(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create two templates with waiting builds
	teamID := testutils.CreateTestTeam(t, db)
	template1ID := testutils.CreateTestTemplate(t, db, teamID)
	template2ID := testutils.CreateTestTemplate(t, db, teamID)

	build1ID := testutils.CreateTestBuild(t, ctx, db, template1ID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, template1ID, build1ID, "default")

	build2ID := testutils.CreateTestBuild(t, ctx, db, template2ID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, template2ID, build2ID, "default")

	// Invalidate only template1's builds
	err := db.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: template1ID,
		Tag:        "default",
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify only template1's build was invalidated
	status1 := testutils.GetBuildStatus(t, ctx, db, build1ID)
	assert.Equal(t, "failed", status1, "Template1's build should be invalidated")

	status2 := testutils.GetBuildStatus(t, ctx, db, build2ID)
	assert.Equal(t, "waiting", status2, "Template2's build should NOT be affected")
}

func TestInvalidateUnstartedTemplateBuilds_DoesNotAffectNonWaitingBuilds(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create template with builds in various states
	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	waitingBuildID := testutils.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, waitingBuildID, "default")

	buildingBuildID := testutils.CreateTestBuild(t, ctx, db, templateID, "building")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildingBuildID, "default")

	uploadedBuildID := testutils.CreateTestBuild(t, ctx, db, templateID, "uploaded")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, uploadedBuildID, "default")

	// Invalidate waiting builds
	err := db.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tag:        "default",
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify only waiting build was invalidated
	waitingStatus := testutils.GetBuildStatus(t, ctx, db, waitingBuildID)
	assert.Equal(t, "failed", waitingStatus, "Waiting build should be invalidated")

	buildingStatus := testutils.GetBuildStatus(t, ctx, db, buildingBuildID)
	assert.Equal(t, "building", buildingStatus, "Building build should NOT be affected")

	uploadedStatus := testutils.GetBuildStatus(t, ctx, db, uploadedBuildID)
	assert.Equal(t, "uploaded", uploadedStatus, "Uploaded build should NOT be affected")
}

func TestInvalidateUnstartedTemplateBuilds_MultipleWaitingBuilds(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create template with multiple waiting builds
	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	build1ID := testutils.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, build1ID, "default")

	build2ID := testutils.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, build2ID, "default")

	build3ID := testutils.CreateTestBuild(t, ctx, db, templateID, "waiting")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, build3ID, "default")

	// Invalidate all waiting builds
	err := db.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		TemplateID: templateID,
		Tag:        "default",
		Reason:     types.BuildReason{Message: "Test invalidation"},
	})
	require.NoError(t, err)

	// Verify all waiting builds were invalidated
	assert.Equal(t, "failed", testutils.GetBuildStatus(t, ctx, db, build1ID), "Build 1 should be invalidated")
	assert.Equal(t, "failed", testutils.GetBuildStatus(t, ctx, db, build2ID), "Build 2 should be invalidated")
	assert.Equal(t, "failed", testutils.GetBuildStatus(t, ctx, db, build3ID), "Build 3 should be invalidated")
}
