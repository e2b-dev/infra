package block

import (
	"fmt"
	"sync"
)

type MockDevice struct {
	blockSize int64
	marker    *Marker
	data      []byte
	mu        sync.RWMutex
}

// NewMockDevice creates a new MockDevice instance with the given data.
// It cannot be resized.
func NewMockDevice(data []byte, blockSize int64, fillMarker bool) *MockDevice {
	device := &MockDevice{
		blockSize: blockSize,
		data:      data,
		marker:    NewMarker(uint(len(data) / int(blockSize))),
	}

	if fillMarker {
		device.Mark(0, int64(len(data)))
	}

	return device
}

func (m *MockDevice) ReadRaw(off, length int64) ([]byte, func(), error) {
	b := make([]byte, length)

	_, err := m.ReadAt(b, off)
	if err != nil {
		return nil, nil, err
	}

	return b, func() {}, nil
}

func (m *MockDevice) ReadAt(p []byte, off int64) (n int, err error) {
	fmt.Printf("ReadAt: off=%d, length=%d\n", off, len(p))

	length := int64(len(p))
	if length+off > int64(len(m.data)) {
		length = int64(len(m.data)) - off
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.IsMarked(off, length) {
		return 0, ErrBytesNotAvailable{}
	}

	n = copy(p, m.data[off:off+length])

	return n, nil
}

// WriteAt can write more than one block at a time.
func (m *MockDevice) WriteAt(p []byte, off int64) (n int, err error) {
	length := int64(len(p))
	if length+off > int64(len(m.data)) {
		length = int64(len(m.data)) - off
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	n = copy(m.data[off:off+length], p)

	m.Mark(off, int64(n))

	return n, nil
}

func (m *MockDevice) Sync() error {
	return nil
}

func (m *MockDevice) Size() (int64, error) {
	return int64(len(m.data)), nil
}

func (m *MockDevice) BlockSize() int64 {
	return m.blockSize
}

func (m *MockDevice) IsMarked(off, length int64) bool {
	for i := off; i < off+length; i += m.blockSize {
		if !m.marker.IsMarked(i / m.blockSize) {
			return false
		}
	}

	return true
}

func (m *MockDevice) Mark(off, length int64) {
	for i := off; i < off+length; i += m.blockSize {
		m.marker.Mark(i / m.blockSize)
	}
}
