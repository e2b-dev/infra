package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCache_MakeChunkFilename(t *testing.T) {
	t.Parallel()

	c := Cache{rootPath: "/cache", chunkSize: 1024, tracer: noopTracer}
	filename := c.makeChunkFilename("a/b/c", 1024*4)
	expected := filepath.Join("/cache", "a/b/c", "000000000004-1024.bin")
	assert.Equal(t, expected, filename)
}

func TestCache_SizeCaches(t *testing.T) {
	t.Parallel()

	inner := NewMockAPI(t)
	inner.EXPECT().Size(mock.Anything, "obj").Return(int64(123), nil).Once()
	inner.EXPECT().String().Return("inner").Maybe()

	c := &Cache{
		rootPath:  t.TempDir(),
		inner:     inner,
		chunkSize: 1024,
		tracer:    noopTracer,
	}

	size, err := c.Size(t.Context(), "obj")
	require.NoError(t, err)
	assert.Equal(t, int64(123), size)

	require.Eventually(t, func() bool {
		_, err := c.readLocalSize(t.Context(), "obj")
		return err == nil
	}, time.Second, 10*time.Millisecond)

	c.inner = nil
	size, err = c.Size(t.Context(), "obj")
	require.NoError(t, err)
	assert.Equal(t, int64(123), size)
}

func TestCache_StoreFile_WritesCache(t *testing.T) {
	t.Parallel()

	data := []byte("hello world")
	root := t.TempDir()
	objectPath := "obj"
	inFilePath := filepath.Join(t.TempDir(), "input.bin")
	require.NoError(t, os.WriteFile(inFilePath, data, 0o600))

	inner := NewMockAPI(t)
	inner.EXPECT().StoreFile(mock.Anything, inFilePath, objectPath, mock.Anything).Return((*FrameTable)(nil), nil)
	inner.EXPECT().String().Return("inner").Maybe()

	flags := NewMockFeatureFlagsClient(t)
	flags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(true)
	flags.EXPECT().IntFlag(mock.Anything, mock.Anything).Return(1)

	c := &Cache{
		rootPath:  root,
		inner:     inner,
		chunkSize: 4,
		flags:     flags,
		tracer:    noopTracer,
	}

	_, err := c.StoreFile(t.Context(), inFilePath, objectPath, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		chunkPath := c.makeChunkFilename(objectPath, 0)
		if _, err := os.Stat(chunkPath); err != nil {
			return false
		}
		if _, err := os.Stat(c.sizeFilename(objectPath)); err != nil {
			return false
		}
		return true
	}, time.Second, 10*time.Millisecond)
}

func TestCache_validateReadAtParams(t *testing.T) {
	t.Parallel()

	testcases := map[string]struct {
		chunkSize, bufferSize, offset int64
		expected                      error
	}{
		"buffer is empty": {
			chunkSize:  1,
			bufferSize: 0,
			offset:     0,
			expected:   ErrBufferTooSmall,
		},
		"buffer is smaller than chunk size": {
			chunkSize:  10,
			bufferSize: 5,
			offset:     0,
		},
		"offset is unaligned": {
			chunkSize:  10,
			bufferSize: 10,
			offset:     3,
			expected:   ErrOffsetUnaligned,
		},
		"buffer is too large (unaligned)": {
			chunkSize:  10,
			bufferSize: 11,
			expected:   ErrBufferTooLarge,
		},
		"buffer is too large (aligned)": {
			chunkSize:  10,
			bufferSize: 20,
			expected:   ErrBufferTooLarge,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := Cache{
				chunkSize: tc.chunkSize,
				tracer:    noopTracer,
			}
			err := c.validateReadAtParams(tc.bufferSize, tc.offset)
			if tc.expected == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.expected)
			}
		})
	}
}
