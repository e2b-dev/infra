package block

type MockDevice struct {
	data []byte
}

// NewMockDevice creates a new MockDevice instance with the given data.
// It cannot be resized.
func NewMockDevice(data []byte) *MockDevice {
	return &MockDevice{
		data: data,
	}
}

func (m *MockDevice) ReadAt(p []byte, off int64) (n int, err error) {
	n = copy(p, m.data[off:])

	return n, nil
}

func (m *MockDevice) WriteAt(p []byte, off int64) (n int, err error) {
	n = copy(m.data[off:], p)

	return n, nil
}
