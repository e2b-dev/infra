package block

import (
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Build struct {
	header         *header.Header
	buildStore     *build.Store
	storeKeySuffix string
}

func NewFromStorage(
	header *header.Header,
	store *build.Store,
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

		// Skip reading when the uuid is nil.
		// We will use this to handle base builds that are already diffs.
		// The passed slice p must start as empty, otherwise we would need to copy the empty values there.
		if *buildID == uuid.Nil {
			n += int(readLength)

			continue
		}

		mappedBuild, err := b.getBuild(buildID)
		if err != nil {
			return 0, fmt.Errorf("failed to get build: %w", err)
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

func (b *Build) getBuild(buildID *uuid.UUID) (io.ReaderAt, error) {
	source, err := b.buildStore.Get(buildID.String() + "/" + b.storeKeySuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to get build from store: %w", err)
	}

	return source, nil
}
