package orchestrator

import (
	"testing"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrchestrator_convertVolumeMounts(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)

	o := Orchestrator{sqlcDB: db.SqlcClient}

	testCases := map[string]struct {
		input    []api.SandboxVolumeMount
		database []queries.CreateVolumeParams
		expected []*orchestrator.SandboxVolumeMount
		err      error
	}{
		"missing volume reports correct error": {
			input: []api.SandboxVolumeMount{
				{Name: "vol1"},
			},
			err: VolumesNotFoundError{[]string{"vol1"}},
		},
		"existing volumes are returned": {
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/vol1"},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
			},
			expected: []*orchestrator.SandboxVolumeMount{
				{Name: "vol1", Path: "/vol1", Type: "local"},
			},
		},
		"partial success returns error": {
			input: []api.SandboxVolumeMount{
				{Name: "vol1", Path: "/vol1"},
				{Name: "vol2", Path: "/vol2"},
			},
			database: []queries.CreateVolumeParams{
				{Name: "vol1", VolumeType: "local"},
			},
			err: VolumesNotFoundError{[]string{"vol2"}},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			teamID := testutils.CreateTestTeam(t, db)

			for _, v := range tc.database {
				_, err := db.SqlcClient.CreateVolume(t.Context(),
					queries.CreateVolumeParams{
						Name:       v.Name,
						TeamID:     teamID,
						VolumeType: v.VolumeType,
					},
				)
				require.NoError(t, err)
			}

			actual, err := o.convertVolumeMounts(t.Context(), teamID, tc.input)
			assert.Equal(t, tc.err, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
