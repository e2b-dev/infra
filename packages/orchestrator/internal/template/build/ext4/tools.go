package ext4

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

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
	args := "-nf"
	if fix {
		args = "-pf"
	}
	cmd := exec.Command("e2fsck", args, rootfsPath)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			var o bytes.Buffer
			o.Write(exitErr.Stderr)
			o.WriteString("\n\n")
			o.Write(out)
			return o.String(), fmt.Errorf("error running e2fsck: %w", err)
		} else {
			return string(out), fmt.Errorf("error running e2fsck: %w", err)
		}
	}

	return strings.TrimSpace(string(out)), nil
}

func Shrink(ctx context.Context, ext4Path string) error {
	cmd := exec.CommandContext(ctx, "resize2fs", "-M", ext4Path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to shrink ext4 image: %w\nOutput: %s", err, string(output))
	}
	return nil
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
