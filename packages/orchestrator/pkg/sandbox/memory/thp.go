//go:build linux

package memory

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var thpTracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/memory/thp")

const (
	thpEnabledPath  = "/sys/kernel/mm/transparent_hugepage/enabled"
	thpDefragPath   = "/sys/kernel/mm/transparent_hugepage/defrag"
	thpShmemEnabled = "/sys/kernel/mm/transparent_hugepage/shmem_enabled"
)

// THPConfig holds the desired THP configuration for a shared memfile region.
type THPConfig struct {
	// Enable advises the kernel to use THP for this region via MADV_HUGEPAGE.
	Enable bool

	// PageSize is the base page size for alignment.
	PageSize int64
}

// DefaultTHPConfig returns the recommended THP configuration.
func DefaultTHPConfig() THPConfig {
	return THPConfig{
		Enable:   true,
		PageSize: int64(unix.Getpagesize()),
	}
}

// AdviseTHP applies MADV_HUGEPAGE to the given memory region, encouraging the
// kernel to back it with transparent huge pages (2MB on x86_64). This reduces
// the page table depth (fewer PTE entries) and the number of minor faults
// during CoW operations.
//
// For shared memfile regions (MAP_PRIVATE), MADV_HUGEPAGE causes the kernel
// to opportunistically collapse 4KB base pages into 2MB huge pages. This is
// particularly effective for the Layer 0 and Layer 1 memfiles which are
// read-mostly and shared across many VMs.
//
// The region [data, data+size) must be page-aligned. Returns the number of
// pages advised.
func AdviseTHP(ctx context.Context, data []byte, size int64) (int64, error) {
	ctx, span := thpTracer.Start(ctx, "advise-thp")
	defer span.End()

	if len(data) == 0 || size <= 0 {
		return 0, nil
	}

	pageSize := int64(unix.Getpagesize())
	alignedStart := int64(0)
	alignedSize := (size / pageSize) * pageSize
	if alignedSize <= 0 {
		return 0, nil
	}

	if int64(len(data)) < alignedStart+alignedSize {
		alignedSize = int64(len(data)) - alignedStart
		if alignedSize <= 0 {
			return 0, nil
		}
	}

	if err := unix.Madvise(data[alignedStart:alignedStart+alignedSize], unix.MADV_HUGEPAGE); err != nil {
		return 0, fmt.Errorf("madvise HUGEPAGE at [0,%d): %w", alignedSize, err)
	}

	pagesAdvised := alignedSize / pageSize
	thpPagesAdvised.Add(ctx, pagesAdvised)
	thpRegionsAdvised.Add(ctx, 1)
	span.SetAttributes(attribute.Int64("thp.pages_advised", pagesAdvised))

	return pagesAdvised, nil
}

// AdviseTHPForLayers applies MADV_HUGEPAGE to each shared layer that has data.
// It is called during sandbox creation after the shared memfile layers are
// mapped. Only Layer 0 (infrastructure) and Layer 1 (runtime) are advised;
// Layer 2 (instance private) uses CoW overlay and benefits less from THP.
func AdviseTHPForLayers(ctx context.Context, layers []SharedMemfileLayer, log logger.Logger) {
	ctx, span := thpTracer.Start(ctx, "advise-thp-layers")
	defer span.End()

	for i, layer := range layers {
		if layer.Data == nil || layer.Size <= 0 {
			continue
		}

		pages, err := AdviseTHP(ctx, layer.Data, layer.Size)
		if err != nil {
			log.Warn(ctx, "THP advise failed for layer, continuing",
				zap.Int("layer_index", i),
				zap.Error(err),
			)
			continue
		}

		log.Debug(ctx, "THP advised for layer",
			zap.Int("layer_index", i),
			zap.Int64("pages_advised", pages),
		)
	}

	span.SetAttributes(attribute.Int("thp.layer_count", len(layers)))
}

// SharedMemfileLayer describes a mapped shared memory layer for THP advising.
type SharedMemfileLayer struct {
	Data []byte
	Size int64
	Name string
}

// HostTHPState reports the host kernel THP configuration for diagnostics.
func HostTHPState() (enabled string, defrag string, shmem string, err error) {
	read := func(path string) (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}

	enabled, err = read(thpEnabledPath)
	if err != nil {
		return "", "", "", fmt.Errorf("read THP enabled: %w", err)
	}

	defrag, _ = read(thpDefragPath)   // optional
	shmem, _ = read(thpShmemEnabled)  // optional

	return enabled, defrag, shmem, nil
}

// IsTHPAvailable returns true if the host kernel supports THP and it is not
// set to "never".
func IsTHPAvailable() bool {
	data, err := os.ReadFile(thpEnabledPath)
	if err != nil {
		return false
	}
	mode := strings.TrimSpace(string(data))
	// Format: "always [madvise] never" — the bracketed one is active.
	return !strings.Contains(mode, "[never]")
}
