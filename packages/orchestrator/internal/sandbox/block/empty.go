package block

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Empty struct {
	header *header.Header
}

func NewEmpty(size int64, blockSize int64, buildID uuid.UUID) (*Empty, error) {
	h := header.NewHeader(header.NewTemplateMetadata(
		buildID,
		uint64(blockSize),
		uint64(size),
	), nil)

	return &Empty{
		header: h,
	}, nil
}

func (e *Empty) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	slice, err := e.Slice(ctx, off, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice mmap: %w", err)
	}

	return copy(p, slice), nil
}

func (e *Empty) Size() (int64, error) {
	return int64(e.header.Metadata.Size), nil
}

func (e *Empty) BlockSize() int64 {
	return int64(e.header.Metadata.BlockSize)
}

func (e *Empty) Close() error {
	return nil
}

func (e *Empty) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	end := off + length
	size := int64(e.header.Metadata.Size)
	if end > size {
		end = size
		length = end - off
	}

	// The Empty device does not have any data, so we return a zeroed slice of the requested length.
	return make([]byte, length), nil
}

func (e *Empty) Header() *header.Header {
	return e.header
}

func (e *Empty) UpdateSize() error {
	return nil
}
