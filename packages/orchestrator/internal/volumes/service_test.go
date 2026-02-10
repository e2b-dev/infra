package volumes

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
)

func TestBuildVolumePath(t *testing.T) {
	t.Parallel()

	const goodVolumeName = "good-vol"
	const goodVolumePath = "/mnt/shared"

	v := VolumeService{
		config: cfg.Config{
			PersistentVolumeMounts: map[string]string{
				goodVolumeName: goodVolumePath,
				"attacker":     "/mnt/path",
			},
		},
	}

	teamID := uuid.NewString()
	volumeID := uuid.NewString()

	testCases := map[string]struct {
		volumeType string
		teamID     string
		volumeID   string

		status   *status.Status
		expected string
	}{
		"valid": {
			volumeType: goodVolumeName,
			teamID:     teamID,
			volumeID:   volumeID,
			expected:   filepath.Join(goodVolumePath, teamID, volumeID),
		},
		"invalid team ID": {
			volumeType: goodVolumeName,
			teamID:     "invalid",
			volumeID:   volumeID,
			status:     status.New(codes.InvalidArgument, `invalid team ID "invalid"`),
		},
		"invalid volume ID": {
			volumeType: goodVolumeName,
			teamID:     teamID,
			volumeID:   "invalid",
			status:     status.New(codes.InvalidArgument, `invalid volume ID "invalid"`),
		},
		"missing team ID": {
			volumeType: goodVolumeName,
			volumeID:   volumeID,
			status:     status.New(codes.InvalidArgument, `invalid team ID ""`),
		},
		"missing volume type": {
			teamID:   teamID,
			volumeID: volumeID,
			status:   status.New(codes.NotFound, `volume type "" not found`),
		},
		"volume type not found": {
			volumeType: "non-existent",
			teamID:     teamID,
			volumeID:   volumeID,
			status:     status.New(codes.NotFound, `volume type "non-existent" not found`),
		},
		"prefix attack": {
			volumeType: "attacker",
			teamID:     "1/../../path1/23f2e6e1-76f6-4cbb-a936-0dcd9190dd84",
			volumeID:   volumeID,
			status:     status.New(codes.InvalidArgument, `invalid team ID "1/../../path1/23f2e6e1-76f6-4cbb-a936-0dcd9190dd84"`),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actualPath, actualStatus := v.buildVolumePath(tc.volumeType, tc.teamID, tc.volumeID)
			assert.Equal(t, tc.status, actualStatus)
			assert.Equal(t, tc.expected, actualPath)
		})
	}
}
