package block

import (
	"fmt"
	"os"
)

// File device used to test functionality of the block storage and NBD.
type FileDevice struct {
	f *os.File
}

func NewFileDevice(path string) (*FileDevice, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o666)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	return &FileDevice{f: f}, nil
}

func (t *FileDevice) BlockSize() int64 {
	return 4096
}

func (t *FileDevice) ReadRaw(off int64, size int64) ([]byte, func(), error) {
	b := make([]byte, size)

	n, err := t.f.ReadAt(b, off)

	return b[:n], func() {}, err
}

func (t *FileDevice) Size() (int64, error) {
	fi, err := t.f.Stat()
	if err != nil {
		return 0, err
	}

	return fi.Size(), nil
}

func (t *FileDevice) Close() error {
	return t.f.Close()
}

func (t *FileDevice) ReadAt(b []byte, off int64) (int, error) {
	n, err := t.f.ReadAt(b, off)
	fmt.Printf("read at %d, size %d, err %v\n", off, len(b), err)

	return n, err
}

func (t *FileDevice) WriteAt(b []byte, off int64) (int, error) {
	n, err := t.f.WriteAt(b, off)
	fmt.Printf("write at %d, size %d, err %v\n", off, len(b), err)

	return n, err
}

func (t *FileDevice) Sync() error {
	return t.f.Sync()
}

func (t *FileDevice) IsMarked(off, len int64) bool {
	return true
}
