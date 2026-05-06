package block

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Empty struct {
	header atomic.Pointer[header.Header]
}

var _ ReadonlyDevice = (*Empty)(nil)

func NewEmpty(size int64, blockSize int64, buildID uuid.UUID) (*Empty, error) {
	h, err := header.NewHeader(header.NewTemplateMetadata(
		buildID,
		uint64(blockSize),
		uint64(size),
	), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create header: %w", err)
	}

	e := &Empty{}
	e.header.Store(h)

	return e, nil
}

func (e *Empty) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	slice, err := e.Slice(ctx, off, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice empty: %w", err)
	}

	return copy(p, slice), nil
}

func (e *Empty) Size(_ context.Context) (int64, error) {
	return int64(e.Header().Metadata.Size), nil
}

func (e *Empty) BlockSize() int64 {
	return int64(e.Header().Metadata.BlockSize)
}

func (e *Empty) Close() error {
	return nil
}

func (e *Empty) Slice(_ context.Context, off, length int64) ([]byte, error) {
	end := off + length
	size := int64(e.Header().Metadata.Size)
	if end > size {
		end = size
		length = end - off
	}

	// The Empty device does not have any data, so we return a zeroed slice of the requested length.
	return make([]byte, length), nil
}

func (e *Empty) Header() *header.Header {
	return e.header.Load()
}

func (e *Empty) SwapHeader(h *header.Header) {
	e.header.Store(h)
}

func (e *Empty) UpdateSize() error {
	return errors.New("update size not supported for empty block")
}
