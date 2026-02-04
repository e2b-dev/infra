package testutils

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var _ block.ReadonlyDevice = (*ZeroDevice)(nil)

type ZeroDevice struct {
	blockSize int64
	size      int64
	header    *header.Header
}

func NewZeroDevice(size int64, blockSize int64) (*ZeroDevice, error) {
	h, err := header.NewHeader(header.NewTemplateMetadata(
		uuid.Nil,
		uint64(blockSize),
		uint64(size),
	),
		[]*header.BuildMap{
			{
				Offset:             0,
				Length:             uint64(size),
				BuildId:            uuid.Nil,
				BuildStorageOffset: 0,
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create header: %w", err)
	}

	return &ZeroDevice{
		size:      size,
		blockSize: blockSize,
		header:    h,
	}, nil
}

func (z *ZeroDevice) ReadAt(_ context.Context, p []byte, _ int64) (n int, err error) {
	clear(p)

	return len(p), nil
}

func (z *ZeroDevice) BlockSize() int64 {
	return z.blockSize
}

func (z *ZeroDevice) Slice(_ context.Context, _, length int64) ([]byte, error) {
	return make([]byte, length), nil
}

func (z *ZeroDevice) Header() *header.Header {
	return z.header
}

func (z *ZeroDevice) Close() error {
	return nil
}

func (z *ZeroDevice) Size(_ context.Context) (int64, error) {
	return z.size, nil
}
