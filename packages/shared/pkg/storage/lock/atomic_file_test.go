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
	t.Run("happy path", func(t *testing.T) {
		expected := []byte("hello")

		tempDir := t.TempDir()
		filename := filepath.Join(tempDir, "test.bin")

		f, err := OpenFile(filename)
		require.NoError(t, err)
		require.NotNil(t, f)

		count, err := f.Write(expected)
		require.NoError(t, err)
		assert.Equal(t, len(expected), count)

		_, err = os.Stat("test.bin")
		require.Error(t, err)
		assert.True(t, os.IsNotExist(err))

		err = f.Close()
		require.NoError(t, err)

		data, err := os.ReadFile(filename)
		require.NoError(t, err)
		assert.Equal(t, expected, data)
	})

	t.Run("two files cannot be opened at the same time", func(t *testing.T) {
		tempDir := t.TempDir()
		filename := filepath.Join(tempDir, "test.bin")

		f1, err := OpenFile(filename)
		require.NoError(t, err)
		t.Cleanup(func() {
			err := f1.Close()
			assert.NoError(t, err)
		})

		f2, err := OpenFile(filename)
		require.ErrorIs(t, err, ErrLockAlreadyHeld)
		assert.Nil(t, f2)

		err = f1.Close()
		require.NoError(t, err)

		f2, err = OpenFile(filename)
		require.NoError(t, err)
		t.Cleanup(func() {
			err := f2.Close()
			assert.NoError(t, err)
		})
	})

	t.Run("missing directory returns error", func(t *testing.T) {
		tempDir := t.TempDir()
		filename := filepath.Join(tempDir, "a", "b", "test.bin")

		_, err := OpenFile(filename)
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}
