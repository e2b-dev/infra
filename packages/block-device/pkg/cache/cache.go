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

func (m *Mmap) syncMarkers(markStart, markEnd int64) {
	if m.fileView == nil {
		return
	}

	start := markStart / block.Size
	end := markEnd / block.Size // Use +block.Size-1 to ensure the last block is included if emptyStart is not a multiple of block.Size

	for blockIdx := start; blockIdx < end; blockIdx++ {
		m.marker.Mark(blockIdx)
	}
}

func (m *Mmap) checkFile(off int64) bool {
	if m.fileView != nil {
		return false
	}

	// We can use the first marked block instead if we want.
	start, end, err := m.fileView.MarkedBlockRange(off)
	if err != nil {
		// It does not matter if the whole file is sparse here.
		return false
	}

	defer m.syncMarkers(start, end)

	return start != off/block.Size
}

func (m *Mmap) ReadAt(b []byte, off int64) (int, error) {
	if m.isMarked(off) {
		return m.mmap.ReadAt(b, off)
	}

	return 0, block.ErrBytesNotAvailable{}
}

func (m *Mmap) WriteAt(b []byte, off int64) (n int, err error) {
	n, err = m.mmap.WriteAt(b, off)
	if err != nil {
		return n, err
	}

	m.marker.Mark(off / block.Size)

	return n, nil
}

func (m *Mmap) Close() error {
	return m.mmap.Close()
}

func (m *Mmap) isMarked(off int64) bool {
	return m.marker.IsMarked(off/block.Size) || m.checkFile(off)
}
