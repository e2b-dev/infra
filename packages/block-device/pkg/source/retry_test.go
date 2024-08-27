package source

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockReaderAt struct {
	readAtFunc func(p []byte, off int64) (n int, err error)
}

func (m *mockReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	return m.readAtFunc(p, off)
}

func TestRetrier_ReadAt(t *testing.T) {
	t.Run("success on first attempt", func(t *testing.T) {
		mockReader := &mockReaderAt{
			readAtFunc: func(p []byte, off int64) (n int, err error) {
				return len(p), nil
			},
		}

		ctx, cancel := context.WithCancel(context.Background())

		retrier := NewRetrier(ctx, mockReader, 3, time.Millisecond)

		defer cancel()

		p := make([]byte, 10)
		n, err := retrier.ReadAt(p, 0)

		assert.NoError(t, err)
		assert.Equal(t, len(p), n)
	})

	t.Run("success after retries", func(t *testing.T) {
		var attempts int

		mockReader := &mockReaderAt{
			readAtFunc: func(p []byte, off int64) (n int, err error) {
				attempts++
				if attempts < 3 {
					return 0, errors.New("mock error")
				}

				return len(p), nil
			},
		}

		ctx, cancel := context.WithCancel(context.Background())

		retrier := NewRetrier(ctx, mockReader, 5, time.Millisecond)

		defer cancel()

		p := make([]byte, 10)
		n, err := retrier.ReadAt(p, 0)

		assert.NoError(t, err)
		assert.Equal(t, len(p), n)
		assert.Equal(t, 3, attempts)
	})

	t.Run("failure after max retries", func(t *testing.T) {
		mockReader := &mockReaderAt{
			readAtFunc: func(p []byte, off int64) (n int, err error) {
				return 0, errors.New("mock error")
			},
		}

		ctx, cancel := context.WithCancel(context.Background())

		retrier := NewRetrier(ctx, mockReader, 3, time.Millisecond)

		defer cancel()

		p := make([]byte, 10)
		n, err := retrier.ReadAt(p, 0)

		assert.Error(t, err)
		assert.Equal(t, 0, n)
	})
}
