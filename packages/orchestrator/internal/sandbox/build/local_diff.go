package build

import (
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

type LocalDiff struct {
	chunker   *block.Chunker
	size      int64
	blockSize int64
	id        string
}

func NewLocalDiff(
	id string,
	blockSize int64,
) *LocalDiff {
	return &LocalDiff{
		blockSize: blockSize,
		id:        id,
	}
}

func (b *LocalDiff) Close() error {
	return b.chunker.Close()
}

func (b *LocalDiff) ReadAt(p []byte, off int64) (int, error) {
	return b.chunker.ReadAt(p, off)
}

func (b *LocalDiff) Size() (int64, error) {
	return b.size, nil
}

func (b *LocalDiff) Slice(off, length int64) ([]byte, error) {
	return b.chunker.Slice(off, length)
}

func (b *LocalDiff) WriteTo(w io.Writer) (int64, error) {
	return b.chunker.WriteTo(w)
}

func (b *LocalDiff) Write(p []byte) (n int, err error) {
	return 0, nil
}
