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
	for n < len(p) {
		destinationOffset := int64(n)
		destinationLength := int64(len(p)) - destinationOffset

		mapping, sourceShift, err := b.header.GetMapping(off + destinationOffset)
		if err != nil {
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		buildReader := b.getBuild(&mapping.BuildId)

		sourceOff := int64(mapping.BuildStorageOffset) + sourceShift
		sourceLength := int64(mapping.Length) - sourceShift

		remainingLength := destinationLength - destinationOffset

		length := min(sourceLength, remainingLength)

		// rangeMessage := fmt.Sprintf("%d-%d", mapping.Offset/b.header.Metadata.BlockSize, (mapping.Offset+mapping.Length-1)/b.header.Metadata.BlockSize)

		// fmt.Printf(
		// 	"(%d bytes left to read, off %d) reading %d bytes from %+v/%+v: [%d:] -> [%d:%d] <> %d (source length: %d, shift: %d)\n",
		// 	len(p)-n,
		// 	off,
		// 	length,
		// 	mapping.BuildId,
		// 	b.storeKeySuffix,
		// 	sourceOff,
		// 	destinationOffset,
		// 	destinationOffset+length,
		// 	n,
		// 	mapping.Length,
		// 	sourceShift,
		// )

		if sourceLength <= 0 {
			fmt.Printf(">>> EOF\n")

			return n, io.EOF
		}

		blockN, err := buildReader.ReadAt(
			p[destinationOffset:destinationOffset+length],
			sourceOff,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		// if b.storeKeySuffix == "memfile" {
		// 	fmt.Println()
		// 	fmt.Printf("%s\n", b.storeKeySuffix)
		// 	fmt.Printf(
		// 		"%-13s [%11d,%11d) = [%11d,%11d) in %s, %d B\n",
		// 		rangeMessage,
		// 		mapping.Offset, mapping.Offset+mapping.Length,
		// 		mapping.BuildStorageOffset, mapping.BuildStorageOffset+mapping.Length, mapping.BuildId.String(), mapping.Length,
		// 	)
		// 	fmt.Printf("- [read] offset: %d, length: %d, block: %d\n", off, len(p), uint64(off)/b.header.Metadata.BlockSize)
		// 	fmt.Printf("- [destination] offset: %d, length: %d, block: %d\n", destinationOffset, length, uint64(destinationOffset)/b.header.Metadata.BlockSize)
		// 	fmt.Printf("- [source] offset: %d, length: %d, block: %d\n", sourceOff, length, uint64(sourceOff)/b.header.Metadata.BlockSize)
		// 	fmt.Printf("- [non-zero bytes] %d\n", len(p)-bytes.Count(p[destinationOffset:destinationOffset+length], []byte("\x00")))
		// 	fmt.Println()
		// }

		n += blockN
	}

	return n, nil
}

func (b *Build) getBuild(buildID *uuid.UUID) io.ReaderAt {
	return b.buildStore.Get(buildID.String() + "/" + b.storeKeySuffix)
}
