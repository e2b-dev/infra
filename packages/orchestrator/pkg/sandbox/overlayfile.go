package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	defaultOverlaySizeMB = 1024
)

// CreateOverlayFile creates a sparse ext4 file to be used as the overlay
// upper device (disk B / /dev/vdb in the guest). The file is sparse so it
// only consumes disk space as the guest writes to it.
func CreateOverlayFile(ctx context.Context, path string, sizeMB int64) error {
	if sizeMB <= 0 {
		sizeMB = defaultOverlaySizeMB
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create overlay file: %w", err)
	}

	if err := f.Truncate(sizeMB * 1024 * 1024); err != nil {
		f.Close()
		os.Remove(path)

		return fmt.Errorf("truncate overlay file: %w", err)
	}
	f.Close()

	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-q", "-F", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(path)

		return fmt.Errorf("mkfs.ext4 failed: %w (output: %s)", err, string(out))
	}

	logger.L().Debug(ctx, "created overlay ext4 file",
		zap.String("path", path),
		zap.Int64("size_mb", sizeMB))

	return nil
}
