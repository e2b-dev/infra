package header

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const (
	PageSize        = 2 << 11
	HugepageSize    = 2 << 20
	RootfsBlockSize = 2 << 11
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage/header")

var (
	EmptyHugePage = make([]byte, HugepageSize)
	EmptyBlock    = make([]byte, RootfsBlockSize)
)

func WriteDiffWithTrace(ctx context.Context, source io.ReaderAt, originalMemfile Slicer, blockSize int64, dirty *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
	_, childSpan := tracer.Start(ctx, "create-diff")
	defer childSpan.End()
	childSpan.SetAttributes(attribute.Int64("dirty.length", int64(dirty.Count())))
	childSpan.SetAttributes(attribute.Int64("block.size", blockSize))

	return writeDiff(source, originalMemfile, blockSize, dirty, diff)
}

func writeDiff(source io.ReaderAt, originalMemfile Slicer, blockSize int64, dirtyBig *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
	b := make([]byte, blockSize)

	empty := bitset.New(0)
	dirty := bitset.New(0)

	for bigPageIdx, e := dirtyBig.NextSet(0); e; bigPageIdx, e = dirtyBig.NextSet(bigPageIdx + 1) {
		_, err := source.ReadAt(b, int64(bigPageIdx)*blockSize)
		if err != nil {
			return nil, fmt.Errorf("error reading from source: %w", err)
		}

		originalPage, err := originalMemfile.Slice(context.Background(), int64(bigPageIdx)*blockSize, blockSize)
		if err != nil {
			return nil, fmt.Errorf("error reading from original memfile: %w", err)
		}

		for i := int64(0); i < blockSize; i += PageSize {
			if bytes.Equal(b[i:i+PageSize], originalPage[i:i+PageSize]) {
				continue
			}

			dirty.Set((uint(bigPageIdx*uint(blockSize)+uint(i)) / PageSize))

			_, err = diff.Write(b[i : i+PageSize])
			if err != nil {
				return nil, fmt.Errorf("error writing to diff: %w", err)
			}
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
