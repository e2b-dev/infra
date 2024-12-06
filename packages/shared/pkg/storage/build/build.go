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
	headerData io.Reader,
	store *Store,
	storeKeySuffix string,
) (*Build, error) {
	h, err := header.Deserialize(headerData)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize header: %w", err)
	}

	return &Build{
		header:         h,
		buildStore:     store,
		storeKeySuffix: storeKeySuffix,
	}, nil
}

func (b *Build) ReadAt(p []byte, off int64) (n int, err error) {
	blocks := header.ListBlocks(off, int64(len(p)), b.header.Metadata.BlockSize)

	for _, blockOff := range blocks {
		mapping, err := b.header.GetMapping(blockOff)
		if err != nil {
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		b, err := b.getBuild(&mapping.BuildId)
		if err != nil {
			return 0, fmt.Errorf("failed to get source: %w", err)
		}

		blockN, err := b.ReadAt(p[n:], int64(mapping.BuildStorageOffset))
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		n += blockN
	}

	return n, nil
}

func (b *Build) getBuild(buildID *uuid.UUID) (io.ReaderAt, error) {
	return b.buildStore.Get(buildID.String() + "/" + b.storeKeySuffix)
}
