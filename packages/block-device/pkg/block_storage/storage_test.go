package block_storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockStorageObject struct {
	reader *bytes.Reader
}

func (m *MockStorageObject) Size() (int64, error) {
	return m.reader.Size(), nil
}

func (m *MockStorageObject) ReadAt(p []byte, off int64) (int, error) {
	return m.reader.ReadAt(p, off)
}

func NewMockStorageObject(size int64) StorageObject {
	data := make([]byte, size)

	rand.Read(data)

	return &MockStorageObject{
		reader: bytes.NewReader(data),
	}
}

func TestBlockStorageReadFromStartByBlock(t *testing.T) {
	ctx := context.Background()

	cachePath := "/tmp/start-by-block.test"
	blockSize := int64(4096)
	size := int64(511 * blockSize)

	object := NewMockStorageObject(size)

	storage, err := New(ctx, object, cachePath, blockSize)
	assert.NoError(t, err)

	b := make([]byte, blockSize)
	testB := make([]byte, blockSize)

	for i := int64(0); i < size; i += blockSize {
		_, err := storage.ReadAt(b, i)
		assert.NoError(t, err)

		_, err = object.ReadAt(testB, i)
		assert.NoError(t, err)

		require.True(t, bytes.Equal(testB, b), "expected to read %x, but read %x", testB, b)
	}
}

func TestBlockStorageReadFromStartByBlock(t *testing.T) {
	ctx := context.Background()

	cachePath := "/tmp/start-by-block.test"
	blockSize := int64(4096)
	size := int64(511 * blockSize)

	object := NewMockStorageObject(size)

	storage, err := New(ctx, object, cachePath, blockSize)
	assert.NoError(t, err)

	b := make([]byte, blockSize)
	testB := make([]byte, blockSize)

	for i := int64(0); i < size; i += blockSize {
		_, err := storage.ReadAt(b, i)
		assert.NoError(t, err)

		_, err = object.ReadAt(testB, i)
		assert.NoError(t, err)

		require.True(t, bytes.Equal(testB, b), "expected to read %x, but read %x", testB, b)
	}
}
