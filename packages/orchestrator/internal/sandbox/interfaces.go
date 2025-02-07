package sandbox

import (
	"context"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"go.opentelemetry.io/otel/trace"
)

// SnapshotProvider defines interface for components needed during snapshotting
type SnapshotProvider interface {
	// VM operations
	PauseVM(ctx context.Context) error
	DisableUffd() error
	CreateVMSnapshot(ctx context.Context, tracer trace.Tracer, snapfilePath, memfilePath string) error

	// Memory operations
	GetDirtyPages() *bitset.BitSet
	GetMemfilePageSize() int64

	// Rootfs operations
	GetRootfsPath() (string, error)
	ExportRootfs(ctx context.Context, diffFile *build.LocalDiffFile, onStop func() error) (*bitset.BitSet, error)
	FlushRootfs(path string) error
}

// TemplateProvider defines interface for accessing template files
type TemplateProvider interface {
	Memfile() (*template.Storage, error)
	Rootfs() (*template.Storage, error)
}
