package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type DiffCreator interface {
	process(ctx context.Context, out io.Writer) (*header.DiffMetadata, error)
}

type RootfsDiffCreator struct {
	rootfs   *rootfs.CowDevice
	stopHook func(context.Context) error
}

func (r *RootfsDiffCreator) process(ctx context.Context, out io.Writer) (*header.DiffMetadata, error) {
	if err := r.rootfs.Flush(ctx); err != nil {
		return nil, fmt.Errorf("failed to flush rootfs: %w", err)
	}

	return r.rootfs.ExportDiff(ctx, out, r.stopHook)
}

type MemoryDiffCreator struct {
	tracer     trace.Tracer
	memfile    *template.LocalFileLink
	dirtyPages *bitset.BitSet
	blockSize  int64
}

func (r *MemoryDiffCreator) process(ctx context.Context, out io.Writer) (*header.DiffMetadata, error) {
	memfileSource, err := os.Open(r.memfile.Path())
	defer memfileSource.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to open memfile: %w", err)
	}

	return header.WriteDiffWithTrace(
		ctx,
		r.tracer,
		memfileSource,
		r.blockSize,
		r.dirtyPages,
		out,
	)
}
