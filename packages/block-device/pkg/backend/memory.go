package backend

type MemoryStorage struct {
	data []byte
}

func NewMemoryStorage(data []byte) *MemoryStorage {
	return &MemoryStorage{
		data: data,
	}
}

func (m *MemoryStorage) ReadAt(p []byte, off int64) (n int, err error) {
	n = copy(p, m.data[off:])
	return n, nil
}

func (m *MemoryStorage) WriteAt(p []byte, off int64) (n int, err error) {
	n = copy(m.data[off:], p)
	return n, nil
}
