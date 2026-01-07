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
	t.Parallel()
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
			t.Parallel()
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
		t.Parallel()
		tempDir := t.TempDir()

		c := NewListingCache(tempDir)
		f, err := c.GetRandomFile()
		require.ErrorIs(t, err, ErrNoFiles)
		assert.Empty(t, f)
	})
}

func TestListingCache_Decache(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		path     string
		cached   []string
		expected []string
	}{
		"not in cache": {
			path:     "c",
			cached:   []string{"a", "b"},
			expected: []string{"a", "b"},
		},
		"in cache": {
			path:     "c",
			cached:   []string{"a", "b", "c"},
			expected: []string{"a", "b"},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			basePath := "/a/b"

			fullPath := filepath.Join(basePath, testCase.path)
			items := buildItemsFromPaths(basePath, testCase.cached)

			lc := ListingCache{
				root: basePath,
				cache: map[string][]cacheEntry{
					basePath: items,
				},
			}

			lc.Decache(fullPath)

			expected := buildItemsFromPaths(basePath, testCase.expected)
			assert.Equal(t, expected, lc.cache[basePath])
		})
	}
}

func buildItemsFromPaths(basePath string, input []string) []cacheEntry {
	var items []cacheEntry
	for _, path := range input {
		items = append(items, cacheEntry{
			path:  filepath.Join(basePath, path),
			isDir: false,
		})
	}

	return items
}

func TestRemoveByIndex(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		input    []string
		index    int
		expected []string
	}{
		"empty": {},
		"only": {
			input:    []string{"one"},
			index:    0,
			expected: []string{},
		},
		"first": {
			input:    []string{"one", "two", "three"},
			index:    0,
			expected: []string{"two", "three"},
		},
		"middle": {
			input:    []string{"one", "two", "three"},
			index:    1,
			expected: []string{"one", "three"},
		},
		"last": {
			input:    []string{"one", "two", "three"},
			index:    2,
			expected: []string{"one", "two"},
		},
		"missing": {
			input:    []string{"one", "two", "three"},
			index:    -1,
			expected: []string{"one", "two", "three"},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actual := removeByIndex(testCase.input, testCase.index)
			assert.Equal(t, testCase.expected, actual)
		})
	}
}
