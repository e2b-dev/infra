package header

import (
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
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

func CreateDiff(source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, out io.Writer) error {
	b := make([]byte, blockSize)

	for i, e := dirty.NextSet(0); e; i, e = dirty.NextSet(i + 1) {
		_, err := source.ReadAt(b, int64(i)*blockSize)
		if err != nil {
			return fmt.Errorf("error reading from source: %w", err)
		}

		_, err = out.Write(b)
		if err != nil {
			return fmt.Errorf("error writing to diff: %w", err)
		}
	}

	return nil
}
