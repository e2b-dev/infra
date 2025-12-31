package header

import (
	"bytes"
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel"
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

func writeDiff(source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) (*DiffMetadata, error) {
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
