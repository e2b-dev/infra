package build

import (
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
)

func CreateDiff(source io.ReaderAt, blockSize int64, dirty *bitset.BitSet, diff io.Writer) error {
	b := make([]byte, blockSize)

	for i, e := dirty.NextSet(0); e; i, e = dirty.NextSet(i + 1) {
		_, err := source.ReadAt(b, int64(i)*blockSize)
		if err != nil {
			return fmt.Errorf("error reading from source: %w", err)
		}

		_, err = diff.Write(b)
		if err != nil {
			return fmt.Errorf("error writing to diff: %w", err)
		}
	}

	return nil
}
