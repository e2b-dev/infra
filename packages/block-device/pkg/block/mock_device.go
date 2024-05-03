package block

import "sync"

type MockDevice struct {
	marker *Marker
	data   []byte
	mu     sync.RWMutex
}

// NewMockDevice creates a new MockDevice instance with the given data.
// It cannot be resized.
func NewMockDevice(data []byte, fillMarker bool) *MockDevice {
	marker := NewMarker(uint(len(data) / int(Size)))

	if fillMarker {
		// For every block in the data, we need to mark the marker.
		for i := int64(0); i < int64(len(data)); i += Size {
			marker.Mark(i / Size)
		}
	}

	return &MockDevice{
		data:   data,
		marker: marker,
	}
}

func (m *MockDevice) ReadAt(p []byte, off int64) (n int, err error) {
	length := int64(len(p))
	if length+off > int64(len(m.data)) {
		length = int64(len(m.data)) - off
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.marker != nil && !m.marker.IsMarked(off/Size) {
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

	if m.marker != nil {
		for i := off; i < off+int64(n); i += Size {
			m.marker.Mark(i / Size)
		}
	}

	return n, nil
}

func (m *MockDevice) Sync() error {
	return nil
}
