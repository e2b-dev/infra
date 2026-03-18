package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestListDir_Depth(t *testing.T) {
	t.Parallel()

	s, basePath, volumeInfo := setupTestService(t)

	// Prepare directory structure:
	// /mnt/shared/team-<teamID>/vol-<volumeID>/dir/test.txt
	// /mnt/shared/team-<teamID>/vol-<volumeID>/dir/deep/test.txt

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

	ctx := t.Context()

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

	t.Run("list non-existent dir", func(t *testing.T) {
		t.Parallel()

		req := &orchestrator.VolumeDirListRequest{
			Volume: volumeInfo,
			Path:   "non-existent-dir",
		}
		_, err := s.ListDir(ctx, req)
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("list depth out of range", func(t *testing.T) {
		t.Parallel()

		req := &orchestrator.VolumeDirListRequest{
			Volume: volumeInfo,
			Path:   "dir",
			Depth:  11,
		}
		_, err := s.ListDir(ctx, req)
		requireGRPCError(t, err, codes.InvalidArgument, orchestrator.UserErrorCode_DEPTH_OUT_OF_RANGE)
	})
}

func TestListDir_BrokenSymlink(t *testing.T) {
	t.Parallel()

	s, basePath, volumeInfo := setupTestService(t)

	// Create a directory with a broken symlink
	err := os.MkdirAll(filepath.Join(basePath, "dir"), 0o755)
	require.NoError(t, err)

	// Create a symlink pointing to a non-existent target
	brokenLinkPath := filepath.Join(basePath, "dir", "broken-link")
	err = os.Symlink("/nonexistent/target", brokenLinkPath)
	require.NoError(t, err)

	// Also create a valid file for comparison
	err = os.WriteFile(filepath.Join(basePath, "dir", "valid.txt"), []byte("test"), 0o644)
	require.NoError(t, err)

	ctx := t.Context()

	t.Run("broken symlink is listed", func(t *testing.T) {
		t.Parallel()

		req := &orchestrator.VolumeDirListRequest{
			Volume: volumeInfo,
			Path:   "dir",
			Depth:  1,
		}
		resp, err := s.ListDir(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// The broken symlink should still appear in the listing
		paths := getPaths(t, resp.GetFiles())
		require.ElementsMatch(t, []string{"/dir/broken-link", "/dir/valid.txt"}, paths)

		// Find the broken symlink entry and verify its symlink target
		for _, item := range resp.GetFiles() {
			if item.GetEntry().GetPath() == "/dir/broken-link" {
				// The symlink should be identified as a symlink type
				require.Equal(t, orchestrator.FileType_FILE_TYPE_SYMLINK, item.GetEntry().GetType())

				// With a broken symlink, EvalSymlinks fails, so SymlinkTarget should contain
				// the deepest resolvable path (the raw target if nothing resolves)
				require.Equal(t, "/nonexistent/target", item.GetEntry().GetSymlinkTarget())
			}
		}
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
