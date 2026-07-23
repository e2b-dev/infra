//go:build linux

package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestUpdatePath(t *testing.T) {
	t.Parallel()

	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("update mode only", func(t *testing.T) {
		t.Parallel()

		filename := "update-mode.txt"
		require.NoError(t, os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644))

		resp, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   filename,
			Mode:   new(uint32(0o600)),
		})
		require.NoError(t, err)
		require.Equal(t, uint32(0o600), resp.GetEntry().GetMode())

		info, err := os.Stat(filepath.Join(tmpdir, filename))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("update uid and gid", func(t *testing.T) {
		t.Parallel()

		filename := "update-owner.txt"
		require.NoError(t, os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644))

		resp, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   filename,
			Uid:    new(uint32(1234)),
			Gid:    new(uint32(5678)),
		})
		require.NoError(t, err)
		require.Equal(t, uint32(1234), resp.GetEntry().GetUid())
		require.Equal(t, uint32(5678), resp.GetEntry().GetGid())

		fs, _, errResponse := s.getFilesystemAndPath(t.Context(), &orchestrator.UpdatePathRequest{Volume: volumeInfo})
		require.Nil(t, errResponse)
		assertDir(t, fs, "/"+filename, 1234, 5678, 0o644)
	})

	t.Run("update uid only leaves gid unchanged", func(t *testing.T) {
		t.Parallel()

		filename := "update-uid-only.txt"
		require.NoError(t, os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644))
		require.NoError(t, os.Chown(filepath.Join(tmpdir, filename), 1000, 2000))

		resp, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   filename,
			Uid:    new(uint32(4321)),
		})
		require.NoError(t, err)
		require.Equal(t, uint32(4321), resp.GetEntry().GetUid())
		require.Equal(t, uint32(2000), resp.GetEntry().GetGid())
	})

	t.Run("update gid only leaves uid unchanged", func(t *testing.T) {
		t.Parallel()

		filename := "update-gid-only.txt"
		require.NoError(t, os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644))
		require.NoError(t, os.Chown(filepath.Join(tmpdir, filename), 1000, 2000))

		resp, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   filename,
			Gid:    new(uint32(8765)),
		})
		require.NoError(t, err)
		require.Equal(t, uint32(1000), resp.GetEntry().GetUid())
		require.Equal(t, uint32(8765), resp.GetEntry().GetGid())
	})

	t.Run("update mode, uid, and gid together", func(t *testing.T) {
		t.Parallel()

		filename := "update-all.txt"
		require.NoError(t, os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644))

		resp, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   filename,
			Mode:   new(uint32(0o640)),
			Uid:    new(uint32(111)),
			Gid:    new(uint32(222)),
		})
		require.NoError(t, err)
		require.Equal(t, uint32(0o640), resp.GetEntry().GetMode())
		require.Equal(t, uint32(111), resp.GetEntry().GetUid())
		require.Equal(t, uint32(222), resp.GetEntry().GetGid())

		fs, _, errResponse := s.getFilesystemAndPath(t.Context(), &orchestrator.UpdatePathRequest{Volume: volumeInfo})
		require.Nil(t, errResponse)
		assertDir(t, fs, "/"+filename, 111, 222, 0o640)
	})

	t.Run("update directory", func(t *testing.T) {
		t.Parallel()

		dirname := "update-dir"
		require.NoError(t, os.Mkdir(filepath.Join(tmpdir, dirname), 0o755))

		resp, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   dirname,
			Mode:   new(uint32(0o700)),
		})
		require.NoError(t, err)
		require.Equal(t, orchestrator.FileType_FILE_TYPE_DIRECTORY, resp.GetEntry().GetType())
		require.Equal(t, uint32(0o700), resp.GetEntry().GetMode())
	})

	t.Run("no fields set only stats", func(t *testing.T) {
		t.Parallel()

		filename := "update-noop.txt"
		require.NoError(t, os.WriteFile(filepath.Join(tmpdir, filename), []byte("test"), 0o644))

		resp, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   filename,
		})
		require.NoError(t, err)
		require.Equal(t, "/"+filename, resp.GetEntry().GetPath())
		require.Equal(t, uint32(0o644), resp.GetEntry().GetMode())
	})

	t.Run("chmod non-existent path", func(t *testing.T) {
		t.Parallel()

		_, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   "does-not-exist-chmod",
			Mode:   new(uint32(0o600)),
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("chown non-existent path", func(t *testing.T) {
		t.Parallel()

		_, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   "does-not-exist-chown",
			Uid:    new(uint32(111)),
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("stat non-existent path", func(t *testing.T) {
		t.Parallel()

		_, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: volumeInfo,
			Path:   "does-not-exist-stat",
		})
		requireGRPCError(t, err, codes.NotFound, orchestrator.UserErrorCode_PATH_NOT_FOUND)
	})

	t.Run("invalid team id", func(t *testing.T) {
		t.Parallel()

		_, err := s.UpdatePath(t.Context(), &orchestrator.UpdatePathRequest{
			Volume: &orchestrator.VolumeInfo{
				VolumeType: volumeType,
				TeamId:     "not-a-uuid",
				VolumeId:   volumeInfo.GetVolumeId(),
			},
			Path: "whatever.txt",
			Mode: new(uint32(0o600)),
		})
		requireGRPCError(t, err, codes.InvalidArgument, orchestrator.UserErrorCode_INVALID_REQUEST)
	})
}
