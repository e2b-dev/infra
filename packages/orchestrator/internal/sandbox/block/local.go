package block

import (
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Local struct {
	f    *os.File
	path string

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

	h := header.NewHeader(header.NewTemplateMetadata(
		buildID,
		uint64(blockSize),
		uint64(info.Size()),
	), nil)

	return &Local{
		f:      f,
		path:   path,
		header: h,
	}, nil
}

func (d *Local) Path() string {
	return d.path
}

func (d *Local) ReadAt(p []byte, off int64) (int, error) {
	slice, err := d.Slice(off, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice mmap: %w", err)
	}

	return copy(p, slice), nil
}

func (d *Local) Size() (int64, error) {
	return int64(d.header.Metadata.Size), nil
}

func (d *Local) BlockSize() int64 {
	return int64(d.header.Metadata.BlockSize)
}

func (d *Local) Close() (e error) {
	defer func() {
		e = errors.Join(e, os.Remove(d.path))
	}()

	err := d.f.Close()
	if err != nil {
		return fmt.Errorf("error closing file: %w", err)
	}

	return nil
}

func (d *Local) Slice(off, length int64) ([]byte, error) {
	end := off + length
	size := int64(d.header.Metadata.Size)
	if end > size {
		end = size
		length = end - off
	}

	out := make([]byte, length)
	_, err := d.f.ReadAt(out, off)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func (d *Local) Header() *header.Header {
	return d.header
}

func (d *Local) UpdateHeaderSize() error {
	info, err := d.f.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	d.header.Metadata.Size = uint64(info.Size())

	return nil
}
