package testutils

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var _ block.ReadonlyDevice = (*BuildDevice)(nil)

type BuildDevice struct {
	*build.File

	header    *header.Header
	blockSize int64
}

func NewBuildDevice(file *build.File, header *header.Header, blockSize int64) *BuildDevice {
	return &BuildDevice{
		File:      file,
		header:    header,
		blockSize: blockSize,
	}
}

func (m *BuildDevice) Close() error {
	return nil
}

func (m *BuildDevice) BlockSize() int64 {
	return m.blockSize
}

func (m *BuildDevice) Header() *header.Header {
	return m.header
}

func (m *BuildDevice) Size(_ context.Context) (int64, error) {
	return int64(m.header.Metadata.Size), nil
}
