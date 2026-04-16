package fc

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultOverlaySizeMB = 1024

// setupOverlayDrive creates the overlay ext4 file (if not already set)
// and attaches it as a second FC drive (/dev/vdb in guest).
func (p *Process) setupOverlayDrive(ctx context.Context, ioEngine *string) error {
	if p.OverlayPath == "" {
		path := p.files.SandboxOverlayPath(p.config.StorageConfig)

		if err := CreateOverlayFile(ctx, path, defaultOverlaySizeMB); err != nil {
			return fmt.Errorf("error creating overlay file: %w", err)
		}

		p.OverlayPath = path
	}

	if err := p.client.setOverlayDrive(ctx, p.OverlayPath, ioEngine); err != nil {
		return fmt.Errorf("error setting overlay drive: %w", err)
	}

	telemetry.ReportEvent(ctx, "set overlay drive")

	return nil
}

// CreateOverlayFile creates a sparse ext4 file for the overlay upper device.
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

		return fmt.Errorf("mkfs.ext4: %w (%s)", err, string(out))
	}

	logger.L().Debug(ctx, "created overlay ext4 file",
		zap.String("path", path),
		zap.Int64("size_mb", sizeMB))

	return nil
}
