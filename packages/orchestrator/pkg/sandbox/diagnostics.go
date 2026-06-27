//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// RuntimeMemoryMetrics is a best-effort, live diagnostic snapshot of the
// Firecracker process backing a sandbox. It is intended for benchmarks and
// node diagnostics; it does not mutate sandbox state.
type RuntimeMemoryMetrics struct {
	ProcessPID int

	PageSizeBytes   int64
	GuestMemoryBytes int64
	GuestPageCount  uint64

	ResidentPages      uint64
	ResidentBytes      uint64
	EmptyResidentPages uint64
	EmptyResidentBytes uint64

	DirtyPages uint64
	DirtyBytes uint64
	DirtyRatio float64
}

// RuntimeMemoryMetrics returns resident and dirty-page information reported by
// Firecracker together with the host PID used by the process memory exporter.
func (s *Sandbox) RuntimeMemoryMetrics(ctx context.Context) (RuntimeMemoryMetrics, error) {
	pageSize := int64(unix.Getpagesize())
	metrics := RuntimeMemoryMetrics{
		PageSizeBytes:   pageSize,
		GuestMemoryBytes: s.Config.RamMB * 1024 * 1024,
	}
	if metrics.GuestMemoryBytes > 0 && pageSize > 0 {
		metrics.GuestPageCount = uint64(header.TotalBlocks(metrics.GuestMemoryBytes, pageSize))
	}

	pid, err := s.process.Pid()
	if err != nil {
		return metrics, fmt.Errorf("get firecracker process pid: %w", err)
	}
	metrics.ProcessPID = pid

	var errs []error

	resident, err := s.process.MemoryInfo(ctx, pageSize)
	if err != nil {
		errs = append(errs, fmt.Errorf("get firecracker resident memory info: %w", err))
	} else {
		metrics.ResidentPages = resident.Dirty.GetCardinality()
		metrics.ResidentBytes = metrics.ResidentPages * uint64(resident.BlockSize)
		metrics.EmptyResidentPages = resident.Empty.GetCardinality()
		metrics.EmptyResidentBytes = metrics.EmptyResidentPages * uint64(resident.BlockSize)
	}

	dirty, err := s.process.DirtyMemory(ctx, pageSize)
	if err != nil {
		errs = append(errs, fmt.Errorf("get firecracker dirty memory info: %w", err))
	} else {
		metrics.DirtyPages = dirty.Dirty.GetCardinality()
		metrics.DirtyBytes = metrics.DirtyPages * uint64(dirty.BlockSize)
		if metrics.GuestMemoryBytes > 0 {
			metrics.DirtyRatio = float64(metrics.DirtyBytes) / float64(metrics.GuestMemoryBytes)
		}
	}

	return metrics, errors.Join(errs...)
}
