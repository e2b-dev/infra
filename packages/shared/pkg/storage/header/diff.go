package header

import (
	"bytes"
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

func CreateDiff(source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) error {
	b := make([]byte, blockSize)

	var empty []byte
	if blockSize == RootfsBlockSize {
		empty = EmptyBlock
	} else {
		empty = EmptyHugePage
	}

	for i, e := dirty.NextSet(0); e; i, e = dirty.NextSet(i + 1) {
		_, err := source.ReadAt(b, int64(i)*blockSize)
		if err != nil {
			return fmt.Errorf("error reading from source: %w", err)
		}

		if bytes.Equal(b, empty) {
			fmt.Printf("empty block %d\n", i)
		}

		_, err = diff.Write(b)
		if err != nil {
			return fmt.Errorf("error writing to diff: %w", err)
		}
	}

	return nil
}
