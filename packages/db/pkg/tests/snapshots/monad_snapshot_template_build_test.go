package snapshots

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func TestGetMonadSnapshotTemplateBuild(t *testing.T) {
	t.Parallel()

	client := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, client)
	baseTemplateID := testutils.CreateTestTemplate(t, client, teamID)
	sourceBuildID := testutils.CreateTestBuild(t, ctx, client, baseTemplateID, "uploaded")
	sandboxID := "sandbox-" + uuid.NewString()
	originNodeID := "node-1"
	envdVersion := "v1.0.0"
	totalDiskSize := int64(1024)

	snapshot, err := client.SqlcClient.UpsertSnapshot(ctx, queries.UpsertSnapshotParams{
		TemplateID:      "pause-" + uuid.NewString(),
		TeamID:          teamID,
		BaseTemplateID:  baseTemplateID,
		SandboxID:       sandboxID,
		StartedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Vcpu:            2,
		RamMb:           2048,
		TotalDiskSizeMb: &totalDiskSize,
		Metadata: types.JSONBStringMap{
			"monad.workcell.provider":          "e2b",
			"monad.workcell.template-id":       baseTemplateID,
			"monad.workcell.image-id":          sourceBuildID.String(),
			"monad.workcell.identity-fidelity": "image-attested",
		},
		KernelVersion:      "6.1.0",
		FirecrackerVersion: "1.4.0",
		EnvdVersion:        &envdVersion,
		OriginNodeID:       originNodeID,
		SourceBuildID:      sourceBuildID,
		Status:             types.BuildStatusUploaded,
	})
	require.NoError(t, err)

	snapshotTemplateID := "snapshot-template-" + uuid.NewString()
	createdID, err := client.SqlcClient.CreateSnapshotTemplateEnv(ctx, queries.CreateSnapshotTemplateEnvParams{
		SnapshotID:   snapshotTemplateID,
		TeamID:       teamID,
		SandboxID:    sandboxID,
		OriginNodeID: &originNodeID,
		BuildID:      &snapshot.BuildID,
		Tag:          "default",
	})
	require.NoError(t, err)
	assert.Equal(t, snapshotTemplateID, createdID)

	lineage, err := client.SqlcClient.GetMonadSnapshotTemplateBuild(ctx, queries.GetMonadSnapshotTemplateBuildParams{
		TemplateID: snapshotTemplateID,
		TeamID:     teamID,
	})
	require.NoError(t, err)
	require.NotNil(t, lineage.BuildID)
	assert.Equal(t, snapshot.BuildID, *lineage.BuildID)
	assert.Equal(t, "default", lineage.Tag)
	assert.Equal(t, types.BuildStatusGroupReady, lineage.StatusGroup)

	_, err = client.SqlcClient.GetMonadSnapshotTemplateBuild(ctx, queries.GetMonadSnapshotTemplateBuildParams{
		TemplateID: snapshotTemplateID,
		TeamID:     uuid.New(),
	})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}
