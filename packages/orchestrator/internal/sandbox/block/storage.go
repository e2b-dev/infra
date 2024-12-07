package block

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/build/header"
)

type Storage struct {
	source *chunker
	size   int64
}

func NewStorage(
	ctx context.Context,
	store *build.Store,
	buildId string,
	storeKeySuffix string,
	blockSize int64,
	cachePath string,
	isSnapshot bool,
) (*Storage, error) {
	id := uuid.MustParse(buildId)

	var h *header.Header

	if isSnapshot {
		headerObject := store.Get(id.String() + "/" + storeKeySuffix + ".header")

		diffHeader, err := header.Deserialize(headerObject)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize header: %w", err)
		}

		h = diffHeader
	} else {
		object := store.Get(id.String() + "/" + storeKeySuffix)

		size, err := object.Size()
		if err != nil {
			return nil, fmt.Errorf("failed to get object size: %w", err)
		}

		h = header.NewHeader(&header.Metadata{
			BuildId:   id,
			Size:      size,
			Version:   1,
			BlockSize: blockSize,
		}, nil)
	}

	b := build.NewFromStorage(h, store, storeKeySuffix)

	chunker, err := newChunker(ctx, h.Metadata.Size, blockSize, b, cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunker: %w", err)
	}

	return &Storage{
		source: chunker,
		size:   h.Metadata.Size,
	}, nil
}

func (d *Storage) ReadAt(p []byte, off int64) (int, error) {
	return d.source.ReadAt(p, off)
}

func (d *Storage) Size() (int64, error) {
	return d.size, nil
}

func (d *Storage) Close() error {
	return d.source.Close()
}

func (d *Storage) Slice(off, length int64) ([]byte, error) {
	return d.source.Slice(off, length)
}
