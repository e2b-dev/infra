package testutils

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var _ block.ReadonlyDevice = (*ZeroDevice)(nil)

type ZeroDevice struct {
	blockSize int64
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
	return nil
}

func (z *ZeroDevice) Close() error {
	return nil
}

func (z *ZeroDevice) Size() (int64, error) {
	return 0, nil
}
