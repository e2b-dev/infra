package sandbox

import (
	"context"
	"errors"
	"io"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type DiffCreator interface {
	process(ctx context.Context, out io.Writer) (*header.DiffMetadata, error)
}

type RootfsDiffCreator struct {
	rootfs    rootfs.Provider
	closeHook func(context.Context) error
}

func (r *RootfsDiffCreator) process(ctx context.Context, out io.Writer) (*header.DiffMetadata, error) {
	return r.rootfs.ExportDiff(ctx, out, r.closeHook)
}

type MemoryDiffCreator struct {
	memory     io.ReaderAt
	dirtyPages *bitset.BitSet
	blockSize  int64
	doneHook   func(context.Context) error
}

func (r *MemoryDiffCreator) process(ctx context.Context, out io.Writer) (h *header.DiffMetadata, e error) {
	defer func() {
		if r.doneHook != nil {
			err := r.doneHook(ctx)
			if err != nil {
				e = errors.Join(e, err)
			}
		}
	}()

	return header.WriteDiffWithTrace(
		ctx,
		r.memory,
		r.blockSize,
		r.dirtyPages,
		out,
	)
}
