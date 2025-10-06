package pkg

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type DiskInfo struct {
	Total, Used int64
}

func GetDiskInfo(ctx context.Context, path string) (DiskInfo, error) {
	// Execute: df <path>
	cmd := exec.CommandContext(ctx, "df", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DiskInfo{}, fmt.Errorf("df command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return DiskInfo{}, fmt.Errorf("unexpected df output: %q", strings.TrimSpace(string(out)))
	}

	// Skip header (line 0) and parse the first data line
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		totalSize, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return DiskInfo{}, fmt.Errorf("failed to parse total size: %w", err)
		}

		usedSpace, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return DiskInfo{}, fmt.Errorf("failed to parse available space: %w", err)
		}

		// "df" returns kilobytes, not bytes
		return DiskInfo{Total: totalSize * 1024, Used: usedSpace * 1024}, nil
	}

	return DiskInfo{}, fmt.Errorf("could not parse mount point from df output: %q", strings.TrimSpace(string(out)))
}
