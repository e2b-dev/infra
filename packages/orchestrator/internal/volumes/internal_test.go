package volumes

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const (
	volumeType = "test-vt"
)

func setupTestService(t *testing.T) (*Service, string, *orchestrator.VolumeInfo) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("Test requires root privileges")
	}

	tmpDir := t.TempDir()
	teamID := uuid.New()
	volumeID := uuid.New()

	config := cfg.Config{
		PersistentVolumeMounts: map[string]string{
			volumeType: tmpDir,
		},
	}

	tracker := chrooted.NewTracker(nil, config)
	t.Cleanup(func() {
		err := tracker.Stop()
		assert.NoError(t, err)
	})

	s := New(config, tracker)

	volumeInfo := &orchestrator.VolumeInfo{
		VolumeType: volumeType,
		TeamId:     teamID.String(),
		VolumeId:   volumeID.String(),
	}

	rootPath, err := s.getVolumeRootPath(t.Context(), volumeInfo)
	require.NoError(t, err)

	err = os.MkdirAll(rootPath, 0o755)
	require.NoError(t, err)

	return s, rootPath, volumeInfo
}
