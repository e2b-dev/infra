package volumes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestListDir_Depth(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	teamID := uuid.NewString()
	volumeID := uuid.NewString()

	// Prepare directory structure:
	// /mnt/shared/team-<teamID>/vol-<volumeID>/dir/test.txt
	// /mnt/shared/team-<teamID>/vol-<volumeID>/dir/deep/test.txt

	basePath := filepath.Join(tmpDir, "team-"+teamID, "vol-"+volumeID)
	err := os.MkdirAll(filepath.Join(basePath, "dir", "deep"), 0o755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(basePath, "dir", "test.txt"), []byte("test"), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(basePath, "dir", "deep", "test.txt"), []byte("deep test"), 0o644)
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Join(basePath, "dir", "deep", "deeper"), 0o755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(basePath, "dir", "deep", "deeper", "test.txt"), []byte("deeper test"), 0o644)
	require.NoError(t, err)

	s := &Service{
		config: cfg.Config{
			PersistentVolumeMounts: map[string]string{
				"shared": tmpDir,
			},
		},
	}

	ctx := context.Background()
	volumeInfo := &orchestrator.VolumeInfo{
		VolumeType: "shared",
		TeamId:     teamID,
		VolumeId:   volumeID,
	}

	t.Run("depth 0", func(t *testing.T) {
		t.Parallel()

		req := &orchestrator.VolumeDirListRequest{
			Volume: volumeInfo,
			Path:   "dir",
			Depth:  0,
		}
		resp, err := s.ListDir(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		paths := getPaths(t, resp.GetFiles())
		require.ElementsMatch(t, []string{"/dir/test.txt", "/dir/deep"}, paths)
	})

	t.Run("depth 1", func(t *testing.T) {
		t.Parallel()

		req := &orchestrator.VolumeDirListRequest{
			Volume: volumeInfo,
			Path:   "dir",
			Depth:  1,
		}
		resp, err := s.ListDir(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		paths := getPaths(t, resp.GetFiles())
		require.ElementsMatch(t, []string{"/dir/test.txt", "/dir/deep"}, paths)
	})

	t.Run("depth 2", func(t *testing.T) {
		t.Parallel()

		req := &orchestrator.VolumeDirListRequest{
			Volume: volumeInfo,
			Path:   "dir",
			Depth:  2,
		}
		resp, err := s.ListDir(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		paths := getPaths(t, resp.GetFiles())
		require.ElementsMatch(t, []string{"/dir/test.txt", "/dir/deep", "/dir/deep/test.txt", "/dir/deep/deeper"}, paths)
	})

	t.Run("depth 3", func(t *testing.T) {
		t.Parallel()

		req := &orchestrator.VolumeDirListRequest{
			Volume: volumeInfo,
			Path:   "dir",
			Depth:  3,
		}
		resp, err := s.ListDir(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		paths := getPaths(t, resp.GetFiles())
		require.ElementsMatch(t, []string{"/dir/test.txt", "/dir/deep", "/dir/deep/test.txt", "/dir/deep/deeper", "/dir/deep/deeper/test.txt"}, paths)
	})
}

func getPaths(t *testing.T, items []*orchestrator.VolumeDirectoryItem) []string {
	t.Helper()

	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.GetEntry().GetPath())
	}

	return paths
}
