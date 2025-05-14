package ext4

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func MakeWritable(ctx context.Context, tracer trace.Tracer, rootfsPath string) error {
	ctx, tuneSpan := tracer.Start(ctx, "tune-rootfs-file-cmd")
	defer tuneSpan.End()

	cmd := exec.CommandContext(ctx, "tune2fs", "-O ^read-only", rootfsPath)

	tuneStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = tuneStdoutWriter

	tuneStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = tuneStderrWriter

	return cmd.Run()
}

func Enlarge(ctx context.Context, tracer trace.Tracer, rootfsPath string, addSize int64) (int64, error) {
	ctx, resizeSpan := tracer.Start(ctx, "resize-rootfs")
	defer resizeSpan.End()

	rootfsFile, err := os.OpenFile(rootfsPath, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("error opening rootfs file: %w", err)
	}
	defer rootfsFile.Close()

	rootfsStats, err := rootfsFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("error statting rootfs file: %w", err)
	}

	telemetry.ReportEvent(ctx, fmt.Sprintf("statted rootfs file (size: %d)", rootfsStats.Size()))

	// In bytes
	rootfsSize := rootfsStats.Size() + addSize

	err = rootfsFile.Truncate(rootfsSize)
	if err != nil {
		return 0, fmt.Errorf("error truncating rootfs file: %w", err)
	}
	telemetry.ReportEvent(ctx, "truncated rootfs file to size of build + defaultDiskSizeMB")

	// Resize the ext4 filesystem
	cmd := exec.CommandContext(ctx, "resize2fs", rootfsPath)
	resizeStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = resizeStdoutWriter
	resizeStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = resizeStderrWriter
	err = cmd.Run()
	if err != nil {
		return 0, fmt.Errorf("error resizing rootfs file: %w", err)
	}

	return rootfsSize, err
}

func CheckIntegrity(rootfsPath string) (string, error) {
	cmd := exec.Command("e2fsck", "-n", rootfsPath)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Error(), fmt.Errorf("error running e2fsck: %w", err)
		} else {
			return string(out), fmt.Errorf("error running e2fsck: %w", err)
		}
	}
	return strings.TrimSpace(string(out)), nil
}
