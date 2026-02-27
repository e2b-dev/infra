package filesystem

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEntryType(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Create test files
	regularFile := filepath.Join(tempDir, "regular.txt")
	require.NoError(t, os.WriteFile(regularFile, []byte("test content"), 0o644))

	testDir := filepath.Join(tempDir, "testdir")
	require.NoError(t, os.MkdirAll(testDir, 0o755))

	symlink := filepath.Join(tempDir, "symlink")
	require.NoError(t, os.Symlink(regularFile, symlink))

	tests := []struct {
		name     string
		path     string
		expected FileType
	}{
		{
			name:     "regular file",
			path:     regularFile,
			expected: FileFileType,
		},
		{
			name:     "directory",
			path:     testDir,
			expected: DirectoryFileType,
		},
		{
			name:     "symlink to file",
			path:     symlink,
			expected: SymlinkFileType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info, err := os.Lstat(tt.path)
			require.NoError(t, err)

			result := getEntryType(info.Mode())
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEntryInfoFromFileInfo_SymlinkChain(t *testing.T) {
	t.Parallel()

	// Base temporary directory. On macOS this lives under /var/folders/…
	// which itself is a symlink to /private/var/folders/….
	tempDir := t.TempDir()

	// Create final target
	target := filepath.Join(tempDir, "target")
	require.NoError(t, os.MkdirAll(target, 0o755))

	// Create a chain: link1 → link2 → target
	link2 := filepath.Join(tempDir, "link2")
	require.NoError(t, os.Symlink(target, link2))

	link1 := filepath.Join(tempDir, "link1")
	require.NoError(t, os.Symlink(link2, link1))

	// run the test
	result, err := GetEntryFromPath(link1)
	require.NoError(t, err)

	// verify the results
	assert.Equal(t, "link1", result.Name)
	assert.Equal(t, link1, result.Path)
	assert.Equal(t, DirectoryFileType, result.Type) // Should resolve to final target type
	assert.Contains(t, result.Permissions, "L")

	// Canonicalize the expected target path to handle macOS symlink indirections
	expectedTarget, err := filepath.EvalSymlinks(link1)
	require.NoError(t, err)
	assert.Equal(t, expectedTarget, *result.SymlinkTarget)
}

func TestEntryInfoFromFileInfo_DifferentPermissions(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	testCases := []struct {
		name           string
		permissions    os.FileMode
		expectedMode   os.FileMode
		expectedString string
	}{
		{"read-only", 0o444, 0o444, "-r--r--r--"},
		{"executable", 0o755, 0o755, "-rwxr-xr-x"},
		{"write-only", 0o200, 0o200, "--w-------"},
		{"no permissions", 0o000, 0o000, "----------"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testFile := filepath.Join(tempDir, tc.name+".txt")
			require.NoError(t, os.WriteFile(testFile, []byte("test"), tc.permissions))

			result, err := GetEntryFromPath(testFile)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedMode, result.Mode)
			assert.Equal(t, tc.expectedString, result.Permissions)
		})
	}
}

func TestEntryInfoFromFileInfo_EmptyFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	emptyFile := filepath.Join(tempDir, "empty.txt")
	require.NoError(t, os.WriteFile(emptyFile, []byte{}, 0o600))

	result, err := GetEntryFromPath(emptyFile)
	require.NoError(t, err)

	assert.Equal(t, "empty.txt", result.Name)
	assert.Equal(t, int64(0), result.Size)
	assert.Equal(t, os.FileMode(0o600), result.Mode)
	assert.Equal(t, FileFileType, result.Type)
}

func TestEntryInfoFromFileInfo_CyclicSymlink(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Create cyclic symlink
	cyclicSymlink := filepath.Join(tempDir, "cyclic")
	require.NoError(t, os.Symlink(cyclicSymlink, cyclicSymlink))

	result, err := GetEntryFromPath(cyclicSymlink)
	require.NoError(t, err)

	assert.Equal(t, "cyclic", result.Name)
	assert.Equal(t, cyclicSymlink, result.Path)
	assert.Equal(t, UnknownFileType, result.Type)
	assert.Contains(t, result.Permissions, "L")
}

func TestEntryInfoFromFileInfo_BrokenSymlink(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Create broken symlink
	brokenSymlink := filepath.Join(tempDir, "broken")
	require.NoError(t, os.Symlink("/nonexistent", brokenSymlink))

	result, err := GetEntryFromPath(brokenSymlink)
	require.NoError(t, err)

	assert.Equal(t, "broken", result.Name)
	assert.Equal(t, brokenSymlink, result.Path)
	assert.Equal(t, UnknownFileType, result.Type)
	assert.Contains(t, result.Permissions, "L")
	// SymlinkTarget might be empty if followSymlink fails
}

func TestEntryInfoFromFileInfo(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Create a regular file with known content and permissions
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := []byte("Hello, World!")
	require.NoError(t, os.WriteFile(testFile, testContent, 0o644))

	// Get current user for ownership comparison
	currentUser, err := user.Current()
	require.NoError(t, err)

	result, err := GetEntryFromPath(testFile)
	require.NoError(t, err)

	// Basic assertions
	assert.Equal(t, "test.txt", result.Name)
	assert.Equal(t, testFile, result.Path)
	assert.Equal(t, int64(len(testContent)), result.Size)
	assert.Equal(t, FileFileType, result.Type)
	assert.Equal(t, os.FileMode(0o644), result.Mode)
	assert.Contains(t, result.Permissions, "-rw-r--r--")
	assert.Equal(t, currentUser.Uid, strconv.Itoa(int(result.UID)))
	assert.Equal(t, currentUser.Gid, strconv.Itoa(int(result.GID)))
	assert.NotNil(t, result.ModifiedTime)
	assert.Empty(t, result.SymlinkTarget)

	// Check that modified time is reasonable (within last minute)
	modTime := result.ModifiedTime
	assert.WithinDuration(t, time.Now(), modTime, time.Minute)
}

func TestEntryInfoFromFileInfo_Directory(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	testDir := filepath.Join(tempDir, "testdir")
	require.NoError(t, os.MkdirAll(testDir, 0o755))

	result, err := GetEntryFromPath(testDir)
	require.NoError(t, err)

	assert.Equal(t, "testdir", result.Name)
	assert.Equal(t, testDir, result.Path)
	assert.Equal(t, DirectoryFileType, result.Type)
	assert.Equal(t, os.FileMode(0o755), result.Mode)
	assert.Equal(t, "drwxr-xr-x", result.Permissions)
	assert.Empty(t, result.SymlinkTarget)
}

func TestEntryInfoFromFileInfo_Symlink(t *testing.T) {
	t.Parallel()

	// Base temporary directory. On macOS this lives under /var/folders/…
	// which itself is a symlink to /private/var/folders/….
	tempDir := t.TempDir()

	// Create target file
	targetFile := filepath.Join(tempDir, "target.txt")
	require.NoError(t, os.WriteFile(targetFile, []byte("target content"), 0o644))

	// Create symlink
	symlinkPath := filepath.Join(tempDir, "symlink")
	require.NoError(t, os.Symlink(targetFile, symlinkPath))

	// Use Lstat to get symlink info (not the target)
	result, err := GetEntryFromPath(symlinkPath)
	require.NoError(t, err)

	assert.Equal(t, "symlink", result.Name)
	assert.Equal(t, symlinkPath, result.Path)
	assert.Equal(t, FileFileType, result.Type)  // Should resolve to target type
	assert.Contains(t, result.Permissions, "L") // Should show as symlink in permissions

	// Canonicalize the expected target path to handle macOS /var → /private/var symlink
	expectedTarget, err := filepath.EvalSymlinks(symlinkPath)
	require.NoError(t, err)
	assert.Equal(t, expectedTarget, *result.SymlinkTarget)
}
