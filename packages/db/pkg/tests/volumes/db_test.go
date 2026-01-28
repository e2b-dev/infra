package volumes

import (
	"testing"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueries_Volumes(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		ctx := t.Context()

		// init database
		result := testutils.SetupDatabase(t)
		db := result.SqlcClient

		teamID := testutils.CreateTestTeam(t, result)

		// create volume = success
		volume, err := db.CreateVolume(ctx, queries.CreateVolumeParams{
			TeamID:     teamID,
			VolumeType: "volume-type",
			Name:       "volume-name",
		})
		require.NoError(t, err)
		assert.Equal(t, "volume-name", volume.Name)
		assert.NotEmpty(t, volume.ID)
		assert.Equal(t, teamID, volume.TeamID)
		assert.Equal(t, "volume-type", volume.VolumeType)

		// create dupe volume = error
		_, err = db.CreateVolume(ctx, queries.CreateVolumeParams{
			TeamID:     teamID,
			VolumeType: "volume-type",
			Name:       "volume-name",
		})
		assert.Error(t, err)

		// get volume = success
		gotVolume, err := db.GetVolume(ctx, queries.GetVolumeParams{
			VolumeID: volume.ID,
			TeamID:   teamID,
		})
		require.NoError(t, err)
		assert.Equal(t, volume, gotVolume)

		// list volume = success
		volumes, err := db.FindVolumesByTeamID(ctx, teamID)
		require.NoError(t, err)
		assert.Len(t, volumes, 1)
		assert.Contains(t, volumes, volume)

		// list volumes by name
		volumesByName, err := db.GetVolumesByName(ctx, queries.GetVolumesByNameParams{
			TeamID:      teamID,
			VolumeNames: []string{"volume-name"},
		})
		require.NoError(t, err)
		assert.Len(t, volumesByName, 1)
		assert.Contains(t, volumesByName, volume)

		// update volume = success
		updatedVolume, err := db.UpdateVolume(ctx, queries.UpdateVolumeParams{
			VolumeID: volume.ID,
			Name:     "new-volume-name",
		})
		require.NoError(t, err)
		assert.Equal(t, volume.ID, updatedVolume.ID)
		assert.Equal(t, "new-volume-name", updatedVolume.Name)

		// get updated volume = success
		gotUpdatedVolume, err := db.GetVolume(ctx, queries.GetVolumeParams{
			VolumeID: volume.ID,
			TeamID:   teamID,
		})
		require.NoError(t, err)
		assert.Equal(t, updatedVolume, gotUpdatedVolume)
	})
}
