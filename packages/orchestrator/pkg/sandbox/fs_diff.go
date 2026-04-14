package sandbox

import (
	"context"
	"fmt"
	"os"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
)

// FSDiff holds the filesystem diff from an FS-only pause.
// It contains only the rootfs CoW mutations — no memory state.
// On FS-only resume, this diff is imported into a fresh overlay cache
// so user files are restored even though processes are lost.
type FSDiff struct {
	RootfsDiff  build.Diff
	DirtyBitset *bitset.BitSet
	BlockSize   int64

	cleanup *Cleanup
}

// DiffFile opens the diff's backing file for reading.
func (d *FSDiff) DiffFile() (*os.File, error) {
	path, err := d.RootfsDiff.CachePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get diff cache path: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open diff file: %w", err)
	}

	return f, nil
}

func (d *FSDiff) Close(ctx context.Context) error {
	var errs []error

	if d.RootfsDiff != nil {
		if err := d.RootfsDiff.Close(); err != nil {
			errs = append(errs, fmt.Errorf("error closing rootfs diff: %w", err))
		}
	}

	if d.cleanup != nil {
		if err := d.cleanup.Run(ctx); err != nil {
			errs = append(errs, fmt.Errorf("error running cleanup: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing FSDiff: %w", errs[0])
	}

	return nil
}
