package header

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("shared.pkg.storage.header")

const (
	PageSize        = 2 << 11
	HugepageSize    = 2 << 20
	RootfsBlockSize = 2 << 11
)

var (
	EmptyHugePage = make([]byte, HugepageSize)
	EmptyBlock    = make([]byte, RootfsBlockSize)
)

func WriteDiff(ctx context.Context, source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
	_, childSpan := tracer.Start(ctx, "create-diff", trace.WithAttributes(
		attribute.Int64("dirty.length", int64(dirty.Count())),
		attribute.Int64("block.size", blockSize),
	))
	defer childSpan.End()

	b := make([]byte, blockSize)

	empty := bitset.New(0)

	compressedDiff := gzip.NewWriter(diff)
	defer compressedDiff.Flush()

	var compressedOffset int
	compressedOffsetMap := make(map[uint64]compressedBlockInfo) // uncompressed -> compressed

	for i, ok := dirty.NextSet(0); ok; i, ok = dirty.NextSet(i + 1) {
		originalOffset := int64(i) * blockSize
		_, err := source.ReadAt(b, originalOffset)
		if err != nil {
			return nil, fmt.Errorf("error reading from source: %w", err)
		}

		// If the block is empty, we don't need to write it to the diff.
		// Because we checked it does not equal to the base, so we keep it separately.
		isEmpty, err := IsEmptyBlock(b, blockSize)
		if err != nil {
			return nil, fmt.Errorf("error checking empty block: %w", err)
		}
		if isEmpty {
			dirty.Clear(i)
			empty.Set(i)

			continue
		}

		count, err := compressedDiff.Write(b)
		if err != nil {
			return nil, fmt.Errorf("error writing to diff: %w", err)
		}
		compressedOffsetMap[uint64(originalOffset)] = compressedBlockInfo{
			offset: uint64(compressedOffset),
			size:   uint64(count),
		}
		compressedOffset += count
	}

	return &DiffMetadata{
		Dirty:     dirty,
		Empty:     empty,
		OffsetMap: compressedOffsetMap,

		BlockSize: blockSize,
	}, nil
}

func IsEmptyBlock(block []byte, blockSize int64) (bool, error) {
	var emptyBuf []byte
	switch blockSize {
	case HugepageSize:
		emptyBuf = EmptyHugePage
	case RootfsBlockSize:
		emptyBuf = EmptyBlock
	default:
		return false, fmt.Errorf("block size not supported: %d", blockSize)
	}

	return bytes.Equal(block, emptyBuf), nil
}
