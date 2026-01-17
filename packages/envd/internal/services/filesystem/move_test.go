package filesystem

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

func TestMove(t *testing.T) {
	t.Parallel()

	// Setup temp root and user
	root := t.TempDir()
	u, err := user.Current()
	require.NoError(t, err)

	// Setup source and destination directories
	sourceDir := filepath.Join(root, "source")
	destDir := filepath.Join(root, "destination")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.MkdirAll(destDir, 0o755))

	// Create a test file to move
	sourceFile := filepath.Join(sourceDir, "test-file.txt")
	testContent := []byte("Hello, World!")
	require.NoError(t, os.WriteFile(sourceFile, testContent, 0o644))

	// Destination file path
	destFile := filepath.Join(destDir, "test-file.txt")

	// Service instance
	svc := mockService()

	// Call the Move function
	ctx := authn.SetInfo(t.Context(), u)
	req := connect.NewRequest(&filesystem.MoveRequest{
		Source:      sourceFile,
		Destination: destFile,
	})
	resp, err := svc.Move(ctx, req)

	// Verify the move was successful
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, destFile, resp.Msg.GetEntry().GetPath())

	// Verify the file exists at the destination
	_, err = os.Stat(destFile)
	require.NoError(t, err)

	// Verify the file no longer exists at the source
	_, err = os.Stat(sourceFile)
	assert.True(t, os.IsNotExist(err))

	// Verify the content of the moved file
	content, err := os.ReadFile(destFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)
}

func TestMoveDirectory(t *testing.T) {
	t.Parallel()

	// Setup temp root and user
	root := t.TempDir()
	u, err := user.Current()
	require.NoError(t, err)

	// Setup source and destination directories
	sourceParent := filepath.Join(root, "source-parent")
	destParent := filepath.Join(root, "dest-parent")
	require.NoError(t, os.MkdirAll(sourceParent, 0o755))
	require.NoError(t, os.MkdirAll(destParent, 0o755))

	// Create a test directory with files to move
	sourceDir := filepath.Join(sourceParent, "test-dir")
	require.NoError(t, os.MkdirAll(filepath.Join(sourceDir, "subdir"), 0o755))

	// Create some files in the directory
	file1 := filepath.Join(sourceDir, "file1.txt")
	file2 := filepath.Join(sourceDir, "subdir", "file2.txt")
	require.NoError(t, os.WriteFile(file1, []byte("File 1 content"), 0o644))
	require.NoError(t, os.WriteFile(file2, []byte("File 2 content"), 0o644))

	// Destination directory path
	destDir := filepath.Join(destParent, "test-dir")

	// Service instance
	svc := mockService()

	// Call the Move function
	ctx := authn.SetInfo(t.Context(), u)
	req := connect.NewRequest(&filesystem.MoveRequest{
		Source:      sourceDir,
		Destination: destDir,
	})
	resp, err := svc.Move(ctx, req)

	// Verify the move was successful
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, destDir, resp.Msg.GetEntry().GetPath())

	// Verify the directory exists at the destination
	_, err = os.Stat(destDir)
	require.NoError(t, err)

	// Verify the files exist at the destination
	destFile1 := filepath.Join(destDir, "file1.txt")
	destFile2 := filepath.Join(destDir, "subdir", "file2.txt")
	_, err = os.Stat(destFile1)
	require.NoError(t, err)
	_, err = os.Stat(destFile2)
	require.NoError(t, err)

	// Verify the directory no longer exists at the source
	_, err = os.Stat(sourceDir)
	assert.True(t, os.IsNotExist(err))

	// Verify the content of the moved files
	content1, err := os.ReadFile(destFile1)
	require.NoError(t, err)
	assert.Equal(t, []byte("File 1 content"), content1)

	content2, err := os.ReadFile(destFile2)
	require.NoError(t, err)
	assert.Equal(t, []byte("File 2 content"), content2)
}

func TestMoveNonExistingFile(t *testing.T) {
	t.Parallel()

	// Setup temp root and user
	root := t.TempDir()
	u, err := user.Current()
	require.NoError(t, err)

	// Setup destination directory
	destDir := filepath.Join(root, "destination")
	require.NoError(t, os.MkdirAll(destDir, 0o755))

	// Non-existing source file
	sourceFile := filepath.Join(root, "non-existing-file.txt")

	// Destination file path
	destFile := filepath.Join(destDir, "moved-file.txt")

	// Service instance
	svc := mockService()

	// Call the Move function
	ctx := authn.SetInfo(t.Context(), u)
	req := connect.NewRequest(&filesystem.MoveRequest{
		Source:      sourceFile,
		Destination: destFile,
	})
	_, err = svc.Move(ctx, req)

	// Verify the correct error is returned
	require.Error(t, err)

	var connectErr *connect.Error
	ok := errors.As(err, &connectErr)
	assert.True(t, ok, "expected error to be of type *connect.Error")
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
	assert.Contains(t, connectErr.Message(), "source file not found")
}

func TestMoveRelativePath(t *testing.T) {
	t.Parallel()

	// Setup user
	u, err := user.Current()
	require.NoError(t, err)

	// Setup directory structure with unique name to avoid conflicts
	testRelativePath := fmt.Sprintf("test-move-%s", uuid.New())
	testFolderPath := filepath.Join(u.HomeDir, testRelativePath)
	require.NoError(t, os.MkdirAll(testFolderPath, 0o755))

	// Create a test file to move
	sourceFile := filepath.Join(testFolderPath, "source-file.txt")
	testContent := []byte("Hello from relative path!")
	require.NoError(t, os.WriteFile(sourceFile, testContent, 0o644))

	// Destination file path (also relative)
	destRelativePath := fmt.Sprintf("test-move-dest-%s", uuid.New())
	destFolderPath := filepath.Join(u.HomeDir, destRelativePath)
	require.NoError(t, os.MkdirAll(destFolderPath, 0o755))
	destFile := filepath.Join(destFolderPath, "moved-file.txt")

	// Service instance
	svc := mockService()

	// Call the Move function with relative paths
	ctx := authn.SetInfo(t.Context(), u)
	req := connect.NewRequest(&filesystem.MoveRequest{
		Source:      filepath.Join(testRelativePath, "source-file.txt"), // Relative path
		Destination: filepath.Join(destRelativePath, "moved-file.txt"),  // Relative path
	})
	resp, err := svc.Move(ctx, req)

	// Verify the move was successful
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, destFile, resp.Msg.GetEntry().GetPath())

	// Verify the file exists at the destination
	_, err = os.Stat(destFile)
	require.NoError(t, err)

	// Verify the file no longer exists at the source
	_, err = os.Stat(sourceFile)
	assert.True(t, os.IsNotExist(err))

	// Verify the content of the moved file
	content, err := os.ReadFile(destFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)

	// Clean up
	os.RemoveAll(testFolderPath)
	os.RemoveAll(destFolderPath)
}

func TestMove_Symlinks(t *testing.T) { //nolint:tparallel // this test cannot be executed in parallel
	root := t.TempDir()
	u, err := user.Current()
	require.NoError(t, err)
	ctx := authn.SetInfo(t.Context(), u)

	// Setup source and destination directories
	sourceRoot := filepath.Join(root, "source")
	destRoot := filepath.Join(root, "destination")
	require.NoError(t, os.MkdirAll(sourceRoot, 0o755))
	require.NoError(t, os.MkdirAll(destRoot, 0o755))

	// 1. Prepare a real directory + file that a symlink will point to
	realDir := filepath.Join(sourceRoot, "real-dir")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	filePath := filepath.Join(realDir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello via symlink"), 0o644))

	// 2. Prepare a standalone real file (points-to-file scenario)
	realFile := filepath.Join(sourceRoot, "real-file.txt")
	require.NoError(t, os.WriteFile(realFile, []byte("i am a plain file"), 0o644))

	// 3. Create symlinks
	linkToDir := filepath.Join(sourceRoot, "link-dir")   // → directory
	linkToFile := filepath.Join(sourceRoot, "link-file") // → file
	require.NoError(t, os.Symlink(realDir, linkToDir))
	require.NoError(t, os.Symlink(realFile, linkToFile))

	svc := mockService()

	t.Run("move symlink to directory", func(t *testing.T) {
		t.Parallel()
		destPath := filepath.Join(destRoot, "moved-link-dir")

		req := connect.NewRequest(&filesystem.MoveRequest{
			Source:      linkToDir,
			Destination: destPath,
		})
		resp, err := svc.Move(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, destPath, resp.Msg.GetEntry().GetPath())

		// Verify the symlink was moved
		_, err = os.Stat(destPath)
		require.NoError(t, err)

		// Verify it's still a symlink
		info, err := os.Lstat(destPath)
		require.NoError(t, err)
		assert.NotEqual(t, 0, info.Mode()&os.ModeSymlink, "expected a symlink")

		// Verify the symlink target is still correct
		target, err := os.Readlink(destPath)
		require.NoError(t, err)
		assert.Equal(t, realDir, target)

		// Verify the original symlink is gone
		_, err = os.Stat(linkToDir)
		assert.True(t, os.IsNotExist(err))

		// Verify the real directory still exists
		_, err = os.Stat(realDir)
		assert.NoError(t, err)
	})

	t.Run("move symlink to file", func(t *testing.T) { //nolint:paralleltest
		destPath := filepath.Join(destRoot, "moved-link-file")

		req := connect.NewRequest(&filesystem.MoveRequest{
			Source:      linkToFile,
			Destination: destPath,
		})
		resp, err := svc.Move(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, destPath, resp.Msg.GetEntry().GetPath())

		// Verify the symlink was moved
		_, err = os.Stat(destPath)
		require.NoError(t, err)

		// Verify it's still a symlink
		info, err := os.Lstat(destPath)
		require.NoError(t, err)
		assert.NotEqual(t, 0, info.Mode()&os.ModeSymlink, "expected a symlink")

		// Verify the symlink target is still correct
		target, err := os.Readlink(destPath)
		require.NoError(t, err)
		assert.Equal(t, realFile, target)

		// Verify the original symlink is gone
		_, err = os.Stat(linkToFile)
		assert.True(t, os.IsNotExist(err))

		// Verify the real file still exists
		_, err = os.Stat(realFile)
		assert.NoError(t, err)
	})

	t.Run("move real file that is target of symlink", func(t *testing.T) {
		t.Parallel()
		// Create a new symlink to the real file
		newLinkToFile := filepath.Join(sourceRoot, "new-link-file")
		require.NoError(t, os.Symlink(realFile, newLinkToFile))

		destPath := filepath.Join(destRoot, "moved-real-file.txt")

		req := connect.NewRequest(&filesystem.MoveRequest{
			Source:      realFile,
			Destination: destPath,
		})
		resp, err := svc.Move(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, destPath, resp.Msg.GetEntry().GetPath())

		// Verify the real file was moved
		_, err = os.Stat(destPath)
		require.NoError(t, err)

		// Verify the original file is gone
		_, err = os.Stat(realFile)
		assert.True(t, os.IsNotExist(err))

		// Verify the symlink still exists but now points to a non-existent file
		_, err = os.Stat(newLinkToFile)
		require.Error(t, err, "symlink should point to non-existent file")
	})
}
