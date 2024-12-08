package build

import (
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/build/header"
	"github.com/google/uuid"
)

type Build struct {
	header         *header.Header
	buildStore     *Store
	storeKeySuffix string
}

func NewFromStorage(
	header *header.Header,
	store *Store,
	storeKeySuffix string,
) *Build {
	return &Build{
		header:         header,
		buildStore:     store,
		storeKeySuffix: storeKeySuffix,
	}
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}

	return b
}

// TODO: Check the list block offsets during copying.
func (b *Build) ReadAt(p []byte, off int64) (n int, err error) {
	var block int64

	for n < len(p) {
		fmt.Printf("n -> %d\n", n)
		block++
		destinationOffset := int64(n)
		destinationLength := int64(len(p)) - destinationOffset

		mapping, err := b.header.GetMapping(off + destinationOffset)
		if err != nil {
			fmt.Printf("failed to get mapping: %v\n", err)
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		buildReader := b.getBuild(&mapping.BuildId)

		sourceShift := off + destinationOffset - int64(mapping.Offset)
		sourceOff := int64(mapping.BuildStorageOffset) + sourceShift
		sourceLength := int64(mapping.Length) - sourceShift

		if sourceLength <= 0 {
			// fmt.Printf("EOF at block %d\n", block)

			return n, io.EOF
		}

		remainingLength := destinationLength - destinationOffset

		length := min(sourceLength, remainingLength)

		blockN, err := buildReader.ReadAt(
			p[destinationOffset:destinationOffset+length],
			sourceOff,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		fmt.Printf(
			"(%d bytes left to read - block %d, off %d) reading %d bytes from %+v/%+v: [%d:] -> [%d:%d] <> %d+%d (source length: %d, shift: %d)\n",
			len(p)-n,
			block,
			off,
			length,
			mapping.BuildId,
			b.storeKeySuffix,
			sourceOff,
			destinationOffset,
			destinationOffset+length,
			n,
			blockN,
			mapping.Length,
			sourceShift,
		)

		n += blockN
	}

	return n, nil
}

func (b *Build) getBuild(buildID *uuid.UUID) io.ReaderAt {
	return b.buildStore.Get(buildID.String() + "/" + b.storeKeySuffix)
}
