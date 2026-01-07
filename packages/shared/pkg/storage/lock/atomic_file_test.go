package lock

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenFile(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()

		expected := []byte("hello")

		tempDir := t.TempDir()
		filename := filepath.Join(tempDir, "test.bin")

		f, err := OpenFile(t.Context(), filename)
		require.NoError(t, err)
		require.NotNil(t, f)

		count, err := f.Write(expected)
		require.NoError(t, err)
		assert.Equal(t, len(expected), count)

		_, err = os.Stat(filename)
		require.Error(t, err)
		assert.True(t, os.IsNotExist(err))

		err = f.Commit(t.Context())
		require.NoError(t, err)

		data, err := os.ReadFile(filename)
		require.NoError(t, err)
		assert.Equal(t, expected, data)
	})

	t.Run("close without commit drops new data", func(t *testing.T) {
		t.Parallel()

		expected := []byte("hello")

		tempDir := t.TempDir()
		filename := filepath.Join(tempDir, "test.bin")

		f, err := OpenFile(t.Context(), filename)
		require.NoError(t, err)
		require.NotNil(t, f)

		count, err := f.Write(expected)
		require.NoError(t, err)
		assert.Equal(t, len(expected), count)

		_, err = os.Stat(filename)
		require.ErrorIs(t, err, os.ErrNotExist)

		err = f.Close(t.Context())
		require.NoError(t, err)

		// destination file does not exist
		_, err = os.Stat(filename)
		require.ErrorIs(t, err, os.ErrNotExist)

		// temp file also does not exist
		_, err = os.Stat(f.tempFile.Name())
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("two files cannot be opened at the same time", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		filename := filepath.Join(tempDir, "test.bin")

		f1, err := OpenFile(t.Context(), filename)
		require.NoError(t, err)
		t.Cleanup(func() {
			err := f1.Close(t.Context())
			assert.NoError(t, err)
		})

		f2, err := OpenFile(t.Context(), filename)
		require.ErrorIs(t, err, ErrLockAlreadyHeld)
		assert.Nil(t, f2)

		err = f1.Close(t.Context())
		require.NoError(t, err)

		f2, err = OpenFile(t.Context(), filename)
		require.NoError(t, err)

		err = f2.Close(t.Context())
		assert.NoError(t, err)
	})

	t.Run("missing directory returns error", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		filename := filepath.Join(tempDir, "a", "b", "test.bin")

		_, err := OpenFile(t.Context(), filename)
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}
