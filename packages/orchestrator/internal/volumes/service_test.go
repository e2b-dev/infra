package volumes

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestBuildVolumePath(t *testing.T) {
	t.Parallel()

	const goodVolumeType = "good-vol"
	const goodVolumeTypePath = "/mnt/shared"
	teamID := uuid.NewString()
	volumeID := uuid.NewString()
	goodVolumeBasePath := filepath.Join(
		goodVolumeTypePath,
		fmt.Sprintf("team-%s", teamID),
		fmt.Sprintf("vol-%s", volumeID),
	)

	v := Service{
		config: cfg.Config{
			PersistentVolumeMounts: map[string]string{
				goodVolumeType: goodVolumeTypePath,
				"attacker":     "/mnt/path",
			},
		},
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		testCases := map[string]struct {
			input string

			expectedFullPath   string
			expectedJailedPath string
			expectedBasePath   string
		}{
			"valid": {
				input:              "",
				expectedBasePath:   goodVolumeBasePath,
				expectedJailedPath: "/",
				expectedFullPath:   goodVolumeBasePath,
			},
			"single dir": {
				input:              "dir",
				expectedBasePath:   goodVolumeBasePath,
				expectedJailedPath: "/dir",
				expectedFullPath:   filepath.Join(goodVolumeBasePath, "dir"),
			},
			"nested path": {
				input:              "a/b/c",
				expectedBasePath:   goodVolumeBasePath,
				expectedJailedPath: "/a/b/c",
				expectedFullPath:   filepath.Join(goodVolumeBasePath, "a", "b", "c"),
			},
			"leading slash treated as relative": {
				input:              "/top/level",
				expectedBasePath:   goodVolumeBasePath,
				expectedJailedPath: "/top/level",
				expectedFullPath:   filepath.Join(goodVolumeBasePath, "top", "level"),
			},
			"clean dot segments": {
				input:              "a/./b/../c/./",
				expectedBasePath:   goodVolumeBasePath,
				expectedJailedPath: "/a/c",
				expectedFullPath:   filepath.Join(goodVolumeBasePath, "a", "c"),
			},
		}

		for name, tc := range testCases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				volumeInfo := orchestrator.VolumeInfo{
					VolumeType: goodVolumeType,
					TeamId:     teamID,
					VolumeId:   volumeID,
				}

				request := orchestrator.VolumeDirCreateRequest{Volume: &volumeInfo, Path: tc.input}
				results, err := v.buildPaths(&request)
				require.NoError(t, err)

				require.Equal(t, tc.expectedFullPath, results.HostFullPath)
				require.Equal(t, tc.expectedJailedPath, results.ClientPath)
				require.Equal(t, tc.expectedBasePath, results.HostVolumePath)
			})
		}
	})

	t.Run("error scenarios", func(t *testing.T) {
		t.Parallel()

		testCases := map[string]struct {
			volumeType string
			teamID     string
			volumeID   string
			relPath    string

			expected *status.Status
		}{
			"invalid team ID": {
				volumeType: goodVolumeType,
				teamID:     "invalid",
				volumeID:   volumeID,
				expected:   status.New(codes.InvalidArgument, `invalid team ID "invalid"`),
			},
			"invalid volume ID": {
				volumeType: goodVolumeType,
				teamID:     teamID,
				volumeID:   "invalid",
				expected:   status.New(codes.InvalidArgument, `invalid volume ID "invalid"`),
			},
			"missing team ID": {
				volumeType: goodVolumeType,
				volumeID:   volumeID,
				expected:   status.New(codes.InvalidArgument, `invalid team ID ""`),
			},
			"missing volume type": {
				teamID:   teamID,
				volumeID: volumeID,
				expected: utils.Must(status.New(codes.NotFound, `volume type "" not found`).WithDetails(&orchestrator.UnknownVolumeTypeError{})),
			},
			"volume type not found": {
				volumeType: "non-existent",
				teamID:     teamID,
				volumeID:   volumeID,
				expected:   utils.Must(status.New(codes.NotFound, `volume type "non-existent" not found`).WithDetails(&orchestrator.UnknownVolumeTypeError{})),
			},
			"prefix attack": {
				volumeType: "attacker",
				teamID:     "1/../../path1/23f2e6e1-76f6-4cbb-a936-0dcd9190dd84",
				volumeID:   volumeID,
				expected:   status.New(codes.InvalidArgument, `invalid team ID "1/../../path1/23f2e6e1-76f6-4cbb-a936-0dcd9190dd84"`),
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
				request := orchestrator.VolumeDirCreateRequest{Volume: &volumeInfo, Path: tc.relPath}
				_, actualStatus := v.buildPaths(&request)
				require.Error(t, actualStatus)
				require.Equal(t, actualStatus.Error(), tc.expected.Err().Error())
			})
		}
	})
}

// TestRelPathTraversal demonstrates whether relPath can be used to traverse outside
// the configured volume mount. We do not call CreateDir/CreateFile to avoid filesystem
// permission side-effects (e.g., chown), but instead validate the resulting joined path.
func TestRelPathTraversal(t *testing.T) {
	t.Parallel()

	// simulate a mount root with a temp dir instead of relying on /mnt/shared
	mountRoot := t.TempDir()

	v := Service{
		config: cfg.Config{
			PersistentVolumeMounts: map[string]string{
				"safe": mountRoot,
			},
		},
	}

	teamID := uuid.New()
	volumeID := uuid.New()
	volumeRootPathParts := append([]string{mountRoot}, BuildVolumePathParts(teamID, volumeID)...)
	volumeRoot := filepath.Join(volumeRootPathParts...)

	tests := map[string]struct {
		rel                string
		expectIsRoot       bool
		expectedClientPath string
		expectedHostPath   string
	}{
		"root":                         {rel: "/", expectIsRoot: true},
		"dot":                          {rel: ".", expectIsRoot: true},
		"empty string":                 {rel: "", expectIsRoot: true},
		"simple traversal":             {rel: "../", expectIsRoot: true},
		"another case":                 {rel: "./a/.././", expectIsRoot: true},
		"simple child":                 {rel: "dir/file.txt", expectedHostPath: volumeRoot + "/dir/file.txt", expectedClientPath: "/dir/file.txt"},
		"parent traversal one level":   {rel: "../escape1", expectedClientPath: "/escape1", expectedHostPath: volumeRoot + "/escape1"},
		"parent traversal many levels": {rel: "../../../../escape2", expectedClientPath: "/escape2", expectedHostPath: volumeRoot + "/escape2"},
		"mixed clean/traverse":         {rel: "./a/.././../escape3", expectedClientPath: "/escape3", expectedHostPath: volumeRoot + "/escape3"},
		"absolute path":                {rel: "/etc/passwd", expectedHostPath: volumeRoot + "/etc/passwd", expectedClientPath: "/etc/passwd"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := orchestrator.VolumeDirCreateRequest{
				Volume: &orchestrator.VolumeInfo{
					VolumeType: "safe",
					TeamId:     teamID.String(),
					VolumeId:   volumeID.String(),
				},
				Path: tc.rel,
			}
			paths, err := v.buildPaths(&request)
			require.NoError(t, err)

			require.NoError(t, err)
			assert.Equal(t, volumeRoot, paths.HostVolumePath)

			if tc.expectIsRoot {
				assert.Empty(t, tc.expectedClientPath)
				assert.Empty(t, tc.expectedHostPath)

				assert.True(t, paths.isRoot(), "result: %v", paths)
				assert.Equal(t, volumeRoot, paths.HostFullPath)
				assert.Equal(t, "/", paths.ClientPath)
			} else {
				assert.False(t, paths.isRoot(), "result: %v", paths)
				assert.Equal(t, tc.expectedClientPath, paths.ClientPath)
				assert.Equal(t, tc.expectedHostPath, paths.HostFullPath)
			}
		})
	}
}
