package utils

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtomicMove_SuccessWhenDestMissing(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()

		td := t.TempDir()
		content := []byte("alpha")
		src := filepath.Join(td, "src")
		dst := filepath.Join(td, "dst")

		require.NoError(t, os.WriteFile(src, content, 0o644))
		err := RenameOrDeleteFile(t.Context(), src, dst)
		require.NoError(t, err)

		// Dest has original content.
		got, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, content, got)

		_, err = os.Stat(src)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("fail when source does not exist", func(t *testing.T) {
		t.Parallel()

		td := t.TempDir()
		src := filepath.Join(td, "src")
		dst := filepath.Join(td, "dst")

		// Operation fails
		err := RenameOrDeleteFile(t.Context(), src, dst)
		require.ErrorIs(t, err, os.ErrNotExist)

		// Destination is not created when RenameOrDeleteFile fails
		_, err = os.Stat(dst)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("fail when destination exists", func(t *testing.T) {
		t.Parallel()

		td := t.TempDir()
		content := []byte("alpha")
		secondContent := []byte("beta")
		src := filepath.Join(td, "src")
		dst := filepath.Join(td, "dst")

		require.NoError(t, os.WriteFile(src, content, 0o644))
		require.NoError(t, os.WriteFile(dst, secondContent, 0o644))
		err := RenameOrDeleteFile(t.Context(), src, dst)
		require.ErrorIs(t, err, os.ErrExist)

		// Dest has original content.
		got, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, secondContent, got)

		// Source is removed when RenameOrDeleteFile fails
		_, err = os.Stat(src)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("succeed when source cannot be removed", func(t *testing.T) {
		t.Parallel()

		errTarget := errors.New("target error")

		td := t.TempDir()
		content := []byte("alpha")
		src := filepath.Join(td, "src")
		dst := filepath.Join(td, "dst")

		// configure mocks
		mocks := newMockfileOps(t)
		mocks.EXPECT().Link(src, dst).Return(nil).Once()
		mocks.EXPECT().Remove(src).Return(errTarget).Once()

		// write the source file
		err := os.WriteFile(src, content, 0o000)
		require.NoError(t, err)

		// should fail
		err = removeOrDeleteFile(t.Context(), src, dst, mocks)
		require.NoError(t, err)
	})
}
