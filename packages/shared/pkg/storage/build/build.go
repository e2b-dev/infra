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

func (b *Build) ReadAt(p []byte, off int64) (n int, err error) {
	for n < len(p) {
		mappedOffset, mappedLength, buildID, err := b.header.GetShiftedMapping(off + int64(n))
		if err != nil {
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		mappedBuild := b.getBuild(buildID)

		remainingReadLength := int64(len(p)) - int64(n)

		readLength := min(mappedLength, remainingReadLength)

		if readLength <= 0 {
			fmt.Printf(
				"(%d bytes left to read, off %d) reading %d bytes from %+v/%+v: [%d:] -> [%d:%d] <> %d (mapped length: %d, remaining read length: %d)\n",
				len(p)-n,
				off,
				readLength,
				buildID,
				b.storeKeySuffix,
				mappedOffset,
				n,
				int64(n)+readLength,
				n,
				mappedLength,
				remainingReadLength,
			)

			fmt.Printf(">>> EOF\n")

			return n, io.EOF
		}

		buildN, err := mappedBuild.ReadAt(
			p[n:int64(n)+readLength],
			mappedOffset,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		n += buildN
	}

	return n, nil
}

func (b *Build) getBuild(buildID *uuid.UUID) io.ReaderAt {
	return b.buildStore.Get(buildID.String() + "/" + b.storeKeySuffix)
}
