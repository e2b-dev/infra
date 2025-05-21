package block

import (
	"errors"
	"fmt"
	"os"

	"github.com/edsrzf/mmap-go"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Local struct {
	m    mmap.MMap
	size int64
	path string

	blockSize int64

	header *header.Header
}

func NewLocal(path string, blockSize int64, buildID uuid.UUID) (*Local, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, os.ModePerm)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	defer f.Close()

	m, err := mmap.Map(f, unix.PROT_READ, mmap.RDONLY)
	if err != nil {
		return nil, fmt.Errorf("failed to map region: %w", err)
	}

	h := header.NewHeader(header.NewTemplateMetadata(
		buildID,
		uint64(blockSize),
		uint64(info.Size()),
	), nil)

	return &Local{
		m:         m,
		size:      info.Size(),
		path:      path,
		blockSize: blockSize,
		header:    h,
	}, nil
}

func (d *Local) ReadAt(p []byte, off int64) (int, error) {
	slice, err := d.Slice(off, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice mmap: %w", err)
	}

	return copy(p, slice), nil
}

func (d *Local) Size() (int64, error) {
	return d.size, nil
}

func (d *Local) BlockSize() int64 {
	return d.blockSize
}

func (d *Local) Close() error {
	return errors.Join(
		d.m.Unmap(),
		os.Remove(d.path),
	)
}

func (d *Local) Slice(off, length int64) ([]byte, error) {
	end := off + length
	if end > d.size {
		end = d.size
	}

	return d.m[off:end], nil
}

func (d *Local) Header() *header.Header {
	return d.header
}
