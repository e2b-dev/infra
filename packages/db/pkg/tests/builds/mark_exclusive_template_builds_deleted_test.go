package builds

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func TestMarkExclusiveTemplateBuildsDeleted_MarksExclusiveBuild(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "success")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	err := db.SqlcClient.MarkExclusiveTemplateBuildsDeleted(ctx, queries.MarkExclusiveTemplateBuildsDeletedParams{
		TemplateID: templateID,
		Reason:     types.BuildReason{Message: "Template deleted by user"},
	})
	require.NoError(t, err)

	assert.Equal(t, "deleted", testutils.GetBuildStatus(t, ctx, db, buildID), "exclusive build should be soft-deleted")
}

func TestMarkExclusiveTemplateBuildsDeleted_LeavesSharedBuild(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	template1ID := testutils.CreateTestTemplate(t, db, teamID)
	template2ID := testutils.CreateTestTemplate(t, db, teamID)

	// One build assigned to both templates (shared).
	sharedBuildID := testutils.CreateTestBuild(t, ctx, db, template1ID, "success")
	testutils.CreateTestBuildAssignment(t, ctx, db, template1ID, sharedBuildID, "default")
	testutils.CreateTestBuildAssignment(t, ctx, db, template2ID, sharedBuildID, "default")

	err := db.SqlcClient.MarkExclusiveTemplateBuildsDeleted(ctx, queries.MarkExclusiveTemplateBuildsDeletedParams{
		TemplateID: template1ID,
		Reason:     types.BuildReason{Message: "Template deleted by user"},
	})
	require.NoError(t, err)

	assert.Equal(t, "success", testutils.GetBuildStatus(t, ctx, db, sharedBuildID), "build shared with another template must not be soft-deleted")
}

func TestMarkExclusiveTemplateBuildsDeleted_DoesNotAffectOtherTemplates(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	template1ID := testutils.CreateTestTemplate(t, db, teamID)
	template2ID := testutils.CreateTestTemplate(t, db, teamID)

	build1ID := testutils.CreateTestBuild(t, ctx, db, template1ID, "success")
	testutils.CreateTestBuildAssignment(t, ctx, db, template1ID, build1ID, "default")

	build2ID := testutils.CreateTestBuild(t, ctx, db, template2ID, "success")
	testutils.CreateTestBuildAssignment(t, ctx, db, template2ID, build2ID, "default")

	err := db.SqlcClient.MarkExclusiveTemplateBuildsDeleted(ctx, queries.MarkExclusiveTemplateBuildsDeletedParams{
		TemplateID: template1ID,
		Reason:     types.BuildReason{Message: "Template deleted by user"},
	})
	require.NoError(t, err)

	assert.Equal(t, "deleted", testutils.GetBuildStatus(t, ctx, db, build1ID), "template1 build should be soft-deleted")
	assert.Equal(t, "success", testutils.GetBuildStatus(t, ctx, db, build2ID), "template2 build should not be affected")
}
