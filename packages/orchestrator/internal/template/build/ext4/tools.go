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

const (
	// creates an inode for every bytes-per-inode byte of space on the disk
	inodesRatio = int64(4096)
	// Percentage of reserved blocks in the filesystem
	reservedBlocksPercentage = int64(0)

	ToMBShift = 20
)

func Make(ctx context.Context, tracer trace.Tracer, rootfsPath string, sizeMb int64, blockSize int64) error {
	ctx, tuneSpan := tracer.Start(ctx, "make-ext4")
	defer tuneSpan.End()

	if blockSize < inodesRatio {
		return fmt.Errorf("block size must be greater than inodes ratio")
	}

	cmd := exec.CommandContext(ctx,
		"mkfs.ext4",
		// Matches the final ext4 features used by tar2ext4 tool
		// But enables resize_inode, sparse_super (default, required for resize_inode)
		"-O", `^has_journal,^dir_index,^64bit,^dir_nlink,^metadata_csum,ext_attr,sparse_super2,filetype,extent,flex_bg,large_file,huge_file,extra_isize`,
		"-b", strconv.FormatInt(blockSize, 10),
		"-m", strconv.FormatInt(reservedBlocksPercentage, 10),
		"-i", strconv.FormatInt(inodesRatio, 10),
		rootfsPath,
		strconv.FormatInt(sizeMb, 10)+"M",
	)

	tuneStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = tuneStdoutWriter

	tuneStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = tuneStderrWriter

	return cmd.Run()
}

func Mount(ctx context.Context, tracer trace.Tracer, rootfsPath string, mountPoint string) error {
	ctx, mountSpan := tracer.Start(ctx, "mount-ext4")
	defer mountSpan.End()

	cmd := exec.CommandContext(ctx, "mount", "-o", "loop", rootfsPath, mountPoint)

	mountStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = mountStdoutWriter

	mountStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = mountStderrWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error mounting ext4 filesystem: %w", err)
	}

	return nil
}

func Unmount(ctx context.Context, tracer trace.Tracer, rootfsPath string) error {
	ctx, unmountSpan := tracer.Start(ctx, "unmount-ext4")
	defer unmountSpan.End()

	cmd := exec.CommandContext(ctx, "umount", rootfsPath)

	unmountStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = unmountStdoutWriter

	unmountStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = unmountStderrWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error unmounting ext4 filesystem: %w", err)
	}

	return nil
}

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
	ctx, resizeSpan := tracer.Start(ctx, "enlarge-ext4")
	defer resizeSpan.End()

	stat, err := os.Stat(rootfsPath)
	if err != nil {
		return 0, fmt.Errorf("error stating rootfs file: %w", err)
	}
	finalSize := stat.Size() + addSize

	return Resize(ctx, tracer, rootfsPath, finalSize)
}

func Resize(ctx context.Context, tracer trace.Tracer, rootfsPath string, targetSize int64) (int64, error) {
	ctx, resizeSpan := tracer.Start(ctx, "resize-ext4")
	defer resizeSpan.End()

	// Resize the ext4 filesystem
	// The underlying file must be synced to the filesystem
	cmd := exec.CommandContext(ctx, "resize2fs", rootfsPath, strconv.FormatInt(targetSize>>ToMBShift, 10)+"M")
	resizeStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = resizeStdoutWriter
	resizeStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = resizeStderrWriter
	err := cmd.Run()
	if err != nil {
		LogMetadata(rootfsPath)
		return 0, fmt.Errorf("error resizing rootfs file: %w", err)
	}

	stat, err := os.Stat(rootfsPath)
	if err != nil {
		return 0, fmt.Errorf("error stating rootfs file after resize: %w", err)
	}

	return stat.Size(), err
}

func Shrink(ctx context.Context, tracer trace.Tracer, rootfsPath string) (int64, error) {
	ctx, resizeSpan := tracer.Start(ctx, "shrink-ext4")
	defer resizeSpan.End()

	// Check the FS integrity first so no errors occur during shrinking
	_, err := CheckIntegrity(rootfsPath, true)
	if err != nil {
		return 0, fmt.Errorf("error checking filesystem integrity before shrink: %w", err)
	}

	// Shrink the ext4 filesystem
	// The underlying file must be synced to the filesystem
	cmd := exec.CommandContext(ctx, "resize2fs", "-M", rootfsPath)
	resizeStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = resizeStdoutWriter
	resizeStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = resizeStderrWriter
	err = cmd.Run()
	if err != nil {
		LogMetadata(rootfsPath)
		return 0, fmt.Errorf("error shrinking rootfs file: %w", err)
	}

	stat, err := os.Stat(rootfsPath)
	if err != nil {
		return 0, fmt.Errorf("error stating rootfs file after resize: %w", err)
	}

	return stat.Size(), err
}

func GetFreeSpace(ctx context.Context, tracer trace.Tracer, rootfsPath string, blockSize int64) (int64, error) {
	_, statSpan := tracer.Start(ctx, "stat-ext4-file")
	defer statSpan.End()

	cmd := exec.Command("debugfs", "-R", "stats", rootfsPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	output := out.String()
	if err != nil {
		zap.L().Error("Error getting free space", zap.Error(err), zap.String("output", output))
		return 0, fmt.Errorf("error statting ext4: %w", err)
	}

	// Extract block size and free blocks
	freeBlocks, err := parseFreeBlocks(output)
	if err != nil {
		return 0, fmt.Errorf("could not parse free blocks: %w", err)
	}

	reservedBlocks, err := parseReservedBlocks(output)
	if err != nil {
		return 0, fmt.Errorf("could not parse reserved blocks: %w", err)
	}

	freeBytes := (freeBlocks - reservedBlocks) * blockSize
	return freeBytes, nil
}

func CheckIntegrity(rootfsPath string, fix bool) (string, error) {
	LogMetadata(rootfsPath)
	accExitCode := 0
	args := "-nfv"
	if fix {
		// 0 - No errors
		// 1 - File system errors corrected
		// 2 - File system errors corrected, a system should be rebooted
		accExitCode = 2
		args = "-pfv"
	}
	cmd := exec.Command("e2fsck", args, rootfsPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		exitCode := cmd.ProcessState.ExitCode()

		if exitCode > accExitCode {
			return string(out), fmt.Errorf("error running e2fsck: %w", err)
		}
	}

	return strings.TrimSpace(string(out)), nil
}

func ReadFile(ctx context.Context, tracer trace.Tracer, rootfsPath string, filePath string) (string, error) {
	_, statSpan := tracer.Start(ctx, "ext4-read-file")
	defer statSpan.End()

	cmd := exec.Command("debugfs", "-R", fmt.Sprintf("cat \"%s\"", filePath), rootfsPath)
	out, err := cmd.Output()
	if err != nil {
		return "1", fmt.Errorf("error reading file: %w", err)
	}

	return string(out), nil
}

func RemoveFile(ctx context.Context, tracer trace.Tracer, rootfsPath string, filePath string) error {
	_, statSpan := tracer.Start(ctx, "ext4-remove-file")
	defer statSpan.End()

	// -w is used to open the filesystem in writable mode
	cmd := exec.Command("debugfs", "-w", "-R", fmt.Sprintf("rm \"%s\"", filePath), rootfsPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		zap.L().Error("error removing file", zap.Error(err), zap.String("output", string(out)))
		return fmt.Errorf("error removing file: %w", err)
	}

	return nil
}

func MountOverlayFS(ctx context.Context, tracer trace.Tracer, layers []string, mountPoint string) error {
	ctx, mountSpan := tracer.Start(ctx, "mount-overlay-fs")
	defer mountSpan.End()

	cmd := exec.CommandContext(ctx, "mount", "-t", "overlay", "overlay", "-o", "lowerdir="+strings.Join(layers, ":"), mountPoint)
	telemetry.ReportEvent(ctx, "mount-ext4-filesystem", attribute.String("cmd", cmd.String()))

	mountStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = mountStdoutWriter

	mountStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = mountStderrWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error mounting ext4 filesystem: %w", err)
	}

	return nil
}

func LogMetadata(rootfsPath string, extraFields ...zap.Field) {
	cmd := exec.Command("tune2fs", "-l", rootfsPath)
	output, err := cmd.CombinedOutput()

	zap.L().With(extraFields...).Debug("tune2fs -l output", zap.String("path", rootfsPath), zap.String("output", string(output)), zap.Error(err))
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

// parseReservedBlocks extracts the "Reserved block count:" value from debugfs output
func parseReservedBlocks(debugfsOutput string) (int64, error) {
	re := regexp.MustCompile(`Reserved block count:\s+(\d+)`)
	matches := re.FindStringSubmatch(debugfsOutput)
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not find reserved blocks in debugfs output")
	}
	reservedBlocks, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse reserved blocks: %w", err)
	}
	return reservedBlocks, nil
}
