package volumes

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestGetVolumeRootPath(t *testing.T) {
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
			},
		},
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		volumeInfo := orchestrator.VolumeInfo{
			VolumeType: goodVolumeType,
			TeamId:     teamID,
			VolumeId:   volumeID,
		}

		path, err := v.getVolumeRootPath(t.Context(), &volumeInfo)
		require.NoError(t, err)
		require.Equal(t, goodVolumeBasePath, path)
	})

	t.Run("error scenarios", func(t *testing.T) {
		t.Parallel()

		type expected struct {
			grpcCode  codes.Code
			userError orchestrator.UserErrorCode
		}

		testCases := map[string]struct {
			volumeType string
			teamID     string
			volumeID   string

			expected expected
		}{
			"invalid team ID": {
				volumeType: goodVolumeType,
				teamID:     "invalid",
				volumeID:   volumeID,
				expected: expected{
					grpcCode:  codes.InvalidArgument,
					userError: orchestrator.UserErrorCode_INVALID_REQUEST,
				},
			},
			"invalid volume ID": {
				volumeType: goodVolumeType,
				teamID:     teamID,
				volumeID:   "invalid",
				expected: expected{
					grpcCode:  codes.InvalidArgument,
					userError: orchestrator.UserErrorCode_INVALID_REQUEST,
				},
			},
			"missing team ID": {
				volumeType: goodVolumeType,
				volumeID:   volumeID,
				expected: expected{
					grpcCode:  codes.InvalidArgument,
					userError: orchestrator.UserErrorCode_INVALID_REQUEST,
				},
			},
			"volume type not found": {
				volumeType: "non-existent",
				teamID:     teamID,
				volumeID:   volumeID,
				expected: expected{
					grpcCode:  codes.Internal,
					userError: orchestrator.UserErrorCode_NOT_SUPPORTED,
				},
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
				_, err := v.getVolumeRootPath(t.Context(), &volumeInfo)
				require.Error(t, err)
				requireGRPCError(t, err, tc.expected.grpcCode, tc.expected.userError)
			})
		}
	})
}

func TestRelPath(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		rel          string
		expectedPath string
	}{
		"root":                         {rel: "/", expectedPath: "/"},
		"dot":                          {rel: ".", expectedPath: "/"},
		"empty string":                 {rel: "", expectedPath: "/"},
		"simple traversal":             {rel: "../", expectedPath: "/"},
		"another case":                 {rel: "./a/.././", expectedPath: "/"},
		"simple child":                 {rel: "dir/file.txt", expectedPath: "/dir/file.txt"},
		"parent traversal one level":   {rel: "../escape1", expectedPath: "/escape1"},
		"parent traversal many levels": {rel: "../../../../escape2", expectedPath: "/escape2"},
		"mixed clean/traverse":         {rel: "./a/.././../escape3", expectedPath: "/escape3"},
		"absolute path":                {rel: "/etc/passwd", expectedPath: "/etc/passwd"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := tc.rel
			if !filepath.IsAbs(path) {
				path = "/" + path
			}
			path = filepath.Clean(path)

			assert.Equal(t, tc.expectedPath, path)
		})
	}
}
