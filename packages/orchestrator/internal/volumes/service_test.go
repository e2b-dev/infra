package volumes

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
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
		relPath    string

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

			volumeInfo := orchestrator.VolumeInfo{
				VolumeType: tc.volumeType,
				TeamId:     tc.teamID,
				VolumeId:   tc.volumeID,
			}
			actualPath, actualStatus := v.buildVolumePath(&volumeInfo, tc.relPath)
			require.ErrorIs(t, tc.status.Err(), actualStatus)
			assert.Equal(t, tc.expected, actualPath)
		})
	}
}

// TestRelPathTraversal demonstrates whether relPath can be used to traverse outside
// the configured volume mount. We do not call CreateDir/CreateFile to avoid filesystem
// permission side-effects (e.g., chown), but instead validate the resulting joined path.
func TestRelPathTraversal(t *testing.T) {
	t.Parallel()

	// simulate a mount root with a temp dir instead of relying on /mnt/shared
	mountRoot := t.TempDir()

	v := VolumeService{
		config: cfg.Config{
			PersistentVolumeMounts: map[string]string{
				"safe": mountRoot,
			},
		},
	}

	teamID := uuid.NewString()
	volumeID := uuid.NewString()

	tests := map[string]struct {
		rel     string
		wantErr bool
	}{
		"simple child":                 {rel: "dir/file.txt", wantErr: false},
		"parent traversal one level":   {rel: "../escape1", wantErr: true},
		"parent traversal many levels": {rel: "../../../../escape2", wantErr: true},
		"mixed clean/traverse":         {rel: "./a/.././../escape3", wantErr: true},
		"absolute path":                {rel: "/etc/passwd", wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := v.buildVolumePath(&orchestrator.VolumeInfo{
				VolumeType: "safe",
				TeamId:     teamID,
				VolumeId:   volumeID,
			}, tc.rel)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
