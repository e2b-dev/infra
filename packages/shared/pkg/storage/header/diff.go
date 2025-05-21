package header

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	PageSize        = 2 << 11
	HugepageSize    = 2 << 20
	RootfsBlockSize = 2 << 11
)

var (
	EmptyHugePage = make([]byte, HugepageSize)
	EmptyBlock    = make([]byte, RootfsBlockSize)
)

func WriteDiffWithTrace(ctx context.Context, tracer trace.Tracer, source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
	_, childSpan := tracer.Start(ctx, "create-diff")
	defer childSpan.End()
	childSpan.SetAttributes(attribute.Int64("dirty.length", int64(dirty.Count())))
	childSpan.SetAttributes(attribute.Int64("block.size", blockSize))

	return WriteDiff(source, blockSize, dirty, diff)
}

func WriteDiff(source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
	b := make([]byte, blockSize)

	empty := bitset.New(0)

	for i, e := dirty.NextSet(0); e; i, e = dirty.NextSet(i + 1) {
		_, err := source.ReadAt(b, int64(i)*blockSize)
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

		_, err = diff.Write(b)
		if err != nil {
			return nil, fmt.Errorf("error writing to diff: %w", err)
		}
	}

	return &DiffMetadata{
		Dirty: dirty,
		Empty: empty,

		BlockSize: blockSize,
	}, nil
}

func IsEmptyBlock(block []byte, blockSize int64) (bool, error) {
	var emptyBuf []byte
	switch {
	case blockSize == HugepageSize:
		emptyBuf = EmptyHugePage
	case blockSize == RootfsBlockSize:
		emptyBuf = EmptyBlock
	default:
		return false, fmt.Errorf("block size not supported: %d", blockSize)
	}

	return bytes.Equal(block, emptyBuf), nil
}
