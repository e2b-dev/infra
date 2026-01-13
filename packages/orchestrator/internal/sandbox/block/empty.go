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

var _ ReadonlyDevice = (*Empty)(nil)

func NewEmpty(size int64, blockSize int64, buildID uuid.UUID) (*Empty, error) {
	metadata := header.NewTemplateMetadata(
		buildID,
		uint64(blockSize),
		uint64(size),
	)

	// Create a single mapping covering the entire size with uuid.Nil
	// This indicates that all data is "empty" (zeros) and should be skipped during reads.
	// When merged with actual dirty mappings, the split parts remain uuid.Nil.
	emptyMapping := []*header.BuildMap{{
		Offset:             0,
		Length:             uint64(size),
		BuildId:            uuid.Nil,
		BuildStorageOffset: 0,
	}}

	h, err := header.NewHeader(metadata, emptyMapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create header: %w", err)
	}

	return &Empty{
		header: h,
	}, nil
}

func (e *Empty) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	slice, err := e.Slice(ctx, off, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice empty: %w", err)
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

func (e *Empty) Slice(_ context.Context, off, length int64) ([]byte, error) {
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
	return fmt.Errorf("update size not supported for empty block")
}
