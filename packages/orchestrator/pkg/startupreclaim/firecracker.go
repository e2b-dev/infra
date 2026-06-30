//go:build linux

package startupreclaim

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/artifact"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// reclaimFirecrackers kills orphaned firecracker process groups by scanning the
// host process table. The network slots they used are reclaimed separately by
// the network reclaim.
func reclaimFirecrackers(ctx context.Context, procDir string) (int, []error) {
	pids, err := discoverFirecrackerPIDs(procDir)
	if err != nil {
		return 0, []error{err}
	}

	reclaimed := 0
	var failures []error
	for _, pid := range pids {
		pgid, err := syscall.Getpgid(pid)
		if err != nil {
			failures = append(failures, fmt.Errorf("failed to get firecracker pgid for pid %d: %w", pid, err))

			continue
		}

		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			failures = append(failures, fmt.Errorf("failed to kill firecracker process group %d for pid %d: %w", pgid, pid, err))

			continue
		}

		reclaimed++
		logger.L().Warn(ctx, "killed orphaned firecracker process group",
			zap.Int("pid", pid),
			zap.Int("pgid", pgid))
	}

	return reclaimed, failures
}

// discoverFirecrackerPIDs scans procDir for leftover firecracker processes.
func discoverFirecrackerPIDs(procDir string) ([]int, error) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read proc directory: %w", err)
	}

	pids := make([]int, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		cmdline, err := processCmdline(procDir, pid)
		if err != nil || !isFirecrackerCmdline(cmdline) {
			continue
		}

		pids = append(pids, pid)
	}

	return pids, nil
}

func processCmdline(procDir string, pid int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}

	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return nil, nil
	}

	parts := bytes.Split(data, []byte{0})
	cmdline := make([]string, 0, len(parts))
	for _, part := range parts {
		cmdline = append(cmdline, string(part))
	}

	return cmdline, nil
}

func isFirecrackerCmdline(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}

	// Exact basename match so versioned paths match but "firecracker-monitor"
	// and similar do not.
	return filepath.Base(cmdline[0]) == artifact.FirecrackerBinaryName
}
