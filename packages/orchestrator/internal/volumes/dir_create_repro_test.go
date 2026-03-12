package volumes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestCreateDir_BugRepro(t *testing.T) {
	// We cannot use t.Parallel() here because setupTestService skips if not root,
	// and we need to be sure we are root to test Chown (though Chmod is enough to prove the bug).
	s, tmpdir, volumeInfo := setupTestService(t)

	t.Run("CreateDir with CreateParents=true should not change existing directory", func(t *testing.T) {
		dirname := "existing-dir-to-preserve"
		fullPath := filepath.Join(tmpdir, dirname)
		
		originalMode := os.FileMode(0o700)
		err := os.MkdirAll(fullPath, originalMode)
		require.NoError(t, err)
		
		// Ensure it's 0700 (MkdirAll might be affected by umask, so Chmod it)
		err = os.Chmod(fullPath, originalMode)
		require.NoError(t, err)

		// Now call CreateDir with CreateParents=true and a different mode
		newMode := uint32(0o777)
		_, err = s.CreateDir(t.Context(), &orchestrator.VolumeDirCreateRequest{
			Volume:        volumeInfo,
			Path:          dirname,
			CreateParents: true,
			Mode:          utils.ToPtr(newMode),
		})
		require.NoError(t, err)

		// Check if the mode was changed
		fi, err := os.Stat(fullPath)
		require.NoError(t, err)
		
		require.Equal(t, originalMode, fi.Mode().Perm(), "Mode should not have been changed for an existing directory when CreateParents=true")
	})
}
