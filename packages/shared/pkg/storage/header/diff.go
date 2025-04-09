package header

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
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

func CreateDiffWithTrace(ctx context.Context, tracer trace.Tracer, source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) (*bitset.BitSet, *bitset.BitSet, error) {
	_, childSpan := tracer.Start(ctx, "create-diff")
	defer childSpan.End()

	return CreateDiff(source, blockSize, dirty, diff)
}

func CreateDiff(source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) (*bitset.BitSet, *bitset.BitSet, error) {
	b := make([]byte, blockSize)

	var emptyBuf []byte
	switch {
	case blockSize == HugepageSize:
		emptyBuf = EmptyHugePage
	case blockSize == RootfsBlockSize:
		emptyBuf = EmptyBlock
	default:
		return nil, nil, fmt.Errorf("block size not supported: %d", blockSize)
	}

	empty := bitset.New(0)

	for i, e := dirty.NextSet(0); e; i, e = dirty.NextSet(i + 1) {
		_, err := source.ReadAt(b, int64(i)*blockSize)
		if err != nil {
			return nil, nil, fmt.Errorf("error reading from source: %w", err)
		}

		// If the block is empty, we don't need to write it to the diff.
		// Because we checked it does not equal to the base, so we keep it separately.
		if bytes.Equal(b, emptyBuf) {
			dirty.Clear(i)
			empty.Set(i)

			continue
		}

		_, err = diff.Write(b)
		if err != nil {
			return nil, nil, fmt.Errorf("error writing to diff: %w", err)
		}
	}

	return dirty, empty, nil
}
