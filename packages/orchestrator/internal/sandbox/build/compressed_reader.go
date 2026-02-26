package build

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/compress"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// compressedSeekable wraps a raw storage object and decompresses reads using
// a frame table from the header. Implements storage.SeekableReader.
type compressedSeekable struct {
	raw    storage.Seekable
	reader compress.SeekableReader
	size   int64
}

func newCompressedSeekable(raw storage.Seekable, ft *header.FrameTable) (*compressedSeekable, error) {
	ra := &storageReaderAt{raw: raw}

	reader, err := compress.NewReaderFromFrames(ra, ft.ToFrameInfo())
	if err != nil {
		return nil, fmt.Errorf("create frame reader: %w", err)
	}

	return &compressedSeekable{raw: raw, reader: reader, size: int64(ft.UncompressedSize)}, nil
}

func (c *compressedSeekable) ReadAt(_ context.Context, buf []byte, off int64) (int, error) {
	return c.reader.ReadAt(buf, off)
}

func (c *compressedSeekable) Size(_ context.Context) (int64, error) {
	return c.size, nil
}

// storageReaderAt adapts storage.Seekable (ctx-based) to io.ReaderAt.
type storageReaderAt struct {
	raw storage.Seekable
}

func (s *storageReaderAt) ReadAt(p []byte, off int64) (int, error) {
	return s.raw.ReadAt(context.Background(), p, off)
}

// compressedBuildDiff is a Diff backed by a chunker that decompresses on
// fetch and caches uncompressed data on disk. Not cached in DiffStore —
// each template creates its own with its pruned frame table.
type compressedBuildDiff struct {
	chunker   *block.Chunker
	blockSz   int64
	cachePath string
}

var _ Diff = (*compressedBuildDiff)(nil)

func (d *compressedBuildDiff) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return d.chunker.ReadAt(ctx, p, off)
}

func (d *compressedBuildDiff) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	return d.chunker.Slice(ctx, off, length)
}

func (d *compressedBuildDiff) Size(_ context.Context) (int64, error) {
	return d.chunker.FileSize()
}

func (d *compressedBuildDiff) FileSize() (int64, error) {
	return d.chunker.FileSize()
}

func (d *compressedBuildDiff) BlockSize() int64       { return d.blockSz }
func (d *compressedBuildDiff) CachePath() (string, error) { return d.cachePath, nil }
func (d *compressedBuildDiff) CacheKey() DiffStoreKey { return "" }
func (d *compressedBuildDiff) Init(context.Context) error { return nil }
func (d *compressedBuildDiff) Close() error               { return d.chunker.Close() }
