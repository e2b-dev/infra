package sandbox

import (
	"context"
	"io"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// SnapshotProvider defines interface for components needed during snapshotting
type SnapshotProvider interface {
	// VM operations
	PauseVM(ctx context.Context) error
	CreateVMSnapshot(ctx context.Context, tracer trace.Tracer, snapfilePath, memfilePath string) error

	// UFFD operations
	DisableUffd() error
	GetDirtyUffd() *bitset.BitSet

	// Memory operations
	GetMemfilePageSize() int64

	// Rootfs operations
	ExportRootfs(ctx context.Context, out io.Writer, stopSandbox func() error) (*bitset.BitSet, error)
	FlushRootfsNBD() error
}

// TemplateProvider defines interface for accessing template files
type TemplateProvider interface {
	MemfileHeader() (*header.Header, error)
	RootfsHeader() (*header.Header, error)
}
