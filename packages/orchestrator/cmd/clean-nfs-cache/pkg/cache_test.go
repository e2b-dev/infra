package pkg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRandomFile(t *testing.T) {
	testCases := map[string]struct {
		file   string
		dirs   []string
		repeat int
	}{
		"flat file": {
			file: "a",
		},
		"nested": {
			file: "a/b",
		},
		"super nested": {
			file: "a/b/c",
		},
		"false positives": {
			file:   "a/b/c",
			dirs:   []string{"b/c", "c/d", "a/a", "a/b/a"},
			repeat: 100,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			tempDir := t.TempDir()

			fullFilePath := filepath.Join(tempDir, tc.file)

			dirName := filepath.Dir(fullFilePath)
			err := os.MkdirAll(dirName, 0o755)
			require.NoError(t, err)

			for _, dir := range tc.dirs {
				dirPath := filepath.Join(tempDir, dir)
				err := os.MkdirAll(dirPath, 0o755)
				require.NoError(t, err)
			}

			filepath.Join(tempDir, tc.file)
			err = os.WriteFile(fullFilePath, []byte(uuid.NewString()), 0o644)
			require.NoError(t, err)

			repeat := tc.repeat
			if repeat == 0 {
				repeat = 1
			}

			for range repeat {
				c := NewListingCache(tempDir)
				f, err := c.GetRandomFile()
				require.NoError(t, err)
				assert.Equal(t, fullFilePath, f)
			}
		})
	}

	t.Run("empty dir", func(t *testing.T) {
		tempDir := t.TempDir()

		c := NewListingCache(tempDir)
		f, err := c.GetRandomFile()
		require.ErrorIs(t, err, ErrNoFiles)
		assert.Empty(t, f)
	})
}
