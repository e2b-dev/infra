package ext4

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func MakeWritable(ctx context.Context, tracer trace.Tracer, rootfsPath string) error {
	ctx, tuneSpan := tracer.Start(ctx, "tune-ext4-writable")
	defer tuneSpan.End()

	cmd := exec.CommandContext(ctx, "tune2fs", "-O ^read-only", rootfsPath)

	tuneStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = tuneStdoutWriter

	tuneStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = tuneStderrWriter

	return cmd.Run()
}

func Enlarge(ctx context.Context, tracer trace.Tracer, rootfsPath string, addSize int64) (int64, error) {
	ctx, resizeSpan := tracer.Start(ctx, "resize-ext4")
	defer resizeSpan.End()

	rootfsSize, err := enlargeFile(ctx, tracer, rootfsPath, addSize)
	if err != nil {
		return 0, fmt.Errorf("error enlarging rootfs file: %w", err)
	}

	// Resize the ext4 filesystem
	// The underlying file must be synced to the filesystem
	cmd := exec.CommandContext(ctx, "resize2fs", rootfsPath)
	resizeStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = resizeStdoutWriter
	resizeStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = resizeStderrWriter
	err = cmd.Run()
	if err != nil {
		logMetadata(rootfsPath)
		return 0, fmt.Errorf("error resizing rootfs file: %w", err)
	}

	return rootfsSize, err
}

func GetFreeSpace(ctx context.Context, tracer trace.Tracer, rootfsPath string, blockSize int64) (int64, error) {
	ctx, statSpan := tracer.Start(ctx, "stat-ext4-file")
	defer statSpan.End()

	cmd := exec.Command("debugfs", "-R", "stats", rootfsPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		zap.L().Error("Error getting free space", zap.Error(err), zap.String("output", out.String()))
		return 0, fmt.Errorf("error statting ext4: %w", err)
	}

	// Extract block size and free blocks
	freeBlocks, err := parseFreeBlocks(out.String())
	if err != nil {
		return 0, fmt.Errorf("could not parse free blocks: %w", err)
	}

	freeBytes := freeBlocks * blockSize
	return freeBytes, nil
}

func CheckIntegrity(rootfsPath string, fix bool) (string, error) {
	logMetadata(rootfsPath)
	args := "-nf"
	if fix {
		args = "-pf"
	}
	cmd := exec.Command("e2fsck", args, rootfsPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("error running e2fsck: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

func logMetadata(rootfsPath string) {
	cmd := exec.Command("tune2fs", "-l", rootfsPath)
	output, err := cmd.CombinedOutput()

	zap.L().Debug("tune2fs -l output", zap.String("path", rootfsPath), zap.String("output", string(output)), zap.Error(err))
}

// parseFreeBlocks extracts the "Free blocks:" value from debugfs output
func parseFreeBlocks(debugfsOutput string) (int64, error) {
	re := regexp.MustCompile(`Free blocks:\s+(\d+)`)
	matches := re.FindStringSubmatch(debugfsOutput)
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not find free blocks in debugfs output")
	}
	freeBlocks, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse free blocks: %w", err)
	}
	return freeBlocks, nil
}

func enlargeFile(ctx context.Context, tracer trace.Tracer, rootfsPath string, addSize int64) (int64, error) {
	ctx, resizeSpan := tracer.Start(ctx, "resize-ext4-file")
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

	// Sync the metadata and data to disk.
	// This is important to ensure that the file is fully written when used by other processes, like FC.
	if err := rootfsFile.Sync(); err != nil {
		return 0, fmt.Errorf("error syncing rootfs file: %w", err)
	}

	telemetry.ReportEvent(ctx, "truncated rootfs file to size of build + defaultDiskSizeMB", attribute.Int64("size", rootfsSize))
	return rootfsSize, err
}
