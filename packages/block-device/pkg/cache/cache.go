package cache

import (
	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
)

type Mmap struct {
	mmap     *mmapedFile
	marker   *block.Marker
	fileView *SparseFileView
}

func NewMmapCache(size int64, filePath string, createFile bool) (*Mmap, error) {
	m, err := newMmappedFile(size, filePath, createFile)
	if err != nil {
		return nil, err
	}

	var sparseMarker *SparseFileView
	if !createFile {
		sparseMarker = NewSparseFileView(m.File)
	}

	return &Mmap{
		mmap:     m,
		marker:   block.NewMarker(uint(size / block.Size)),
		fileView: sparseMarker,
	}, nil
}

func (m *Mmap) ReadAt(b []byte, off int64) (int, error) {
	if m.IsMarked(off) {
		return m.mmap.ReadAt(b, off)
	}

	return 0, block.ErrBytesNotAvailable{}
}

// WriteAt can write more than one block at a time.
func (m *Mmap) WriteAt(b []byte, off int64) (n int, err error) {
	n, err = m.mmap.WriteAt(b, off)
	if err != nil {
		return n, err
	}

	for i := off; i < off+int64(n); i += block.Size {
		m.marker.Mark(i / block.Size)
	}

	return n, nil
}

func (m *Mmap) Close() error {
	return m.mmap.Close()
}

func (m *Mmap) IsMarked(off int64) bool {
	markedInMemory := m.marker.IsMarked(off / block.Size)
	if markedInMemory {
		return true
	}

	if m.fileView == nil {
		return false
	}

	markedInFile, err := m.fileView.IsMarked(off)
	if err != nil {
		return false
	}

	m.marker.Mark(off / block.Size)

	return markedInFile
}
