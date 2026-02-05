package volumes

import (
	"os"
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

func TestCannotTraverse(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		volumeID string
	}{
		"prefix":        {"../canary.txt"},
		"rename-volume": {"a/b/../../canary.txt"},
		"escape":        {"a/b/../../../canary.txt"},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			goodPath := filepath.Join(tempDir, "canary.txt")
			goodContent := uuid.NewString()
			writeFile(t, goodPath, goodContent)

			config := cfg.Config{
				PersistentVolumeMounts: map[string]string{
					"vol1": tempDir,
				},
			}

			s := New(config)

			_, err := s.Delete(t.Context(), &orchestrator.VolumeDeleteRequest{
				VolumeId:   tc.volumeID,
				VolumeType: "vol1",
				TeamId:     "team1",
			})
			require.Error(t, err)
			deleteStatus, ok := status.FromError(err)

			require.True(t, ok)
			require.Equal(t, codes.InvalidArgument, deleteStatus.Code())

			content := readFile(t, goodPath)
			assert.Equal(t, goodContent, content)
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}

func writeFile(t *testing.T, path string, s string) {
	t.Helper()

	err := os.WriteFile(path, []byte(s), 0o644)
	require.NoError(t, err)
}
