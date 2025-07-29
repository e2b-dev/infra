package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type DiffCreator interface {
	process(ctx context.Context, out io.Writer) (*header.DiffMetadata, error)
}

type RootfsDiffCreator struct {
	rootfs   rootfs.Provider
	stopHook func(context.Context) error
}

func (r *RootfsDiffCreator) process(ctx context.Context, out io.Writer) (*header.DiffMetadata, error) {
	return r.rootfs.ExportDiff(ctx, out, r.stopHook)
}

type MemoryDiffCreator struct {
	memfile    *storage.TemporaryMemfile
	dirtyPages *bitset.BitSet
	blockSize  int64
	doneHook   func(context.Context) error
}

func (r *MemoryDiffCreator) process(ctx context.Context, out io.Writer) (h *header.DiffMetadata, e error) {
	defer func() {
		err := r.doneHook(ctx)
		if err != nil {
			e = errors.Join(e, err)
		}
	}()

	memfileSource, err := os.Open(r.memfile.Path())
	if err != nil {
		return nil, fmt.Errorf("failed to open memfile: %w", err)
	}
	defer memfileSource.Close()

	return header.WriteDiff(
		ctx,
		memfileSource,
		r.blockSize,
		r.dirtyPages,
		out,
	)
}
