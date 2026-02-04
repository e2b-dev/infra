package filesystem

import (
	"context"
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

func TestListDir(t *testing.T) {
	t.Parallel()

	// Setup temp root and user
	root := t.TempDir()
	u, err := user.Current()
	require.NoError(t, err)

	// Setup directory structure
	testFolder := filepath.Join(root, "test")
	require.NoError(t, os.MkdirAll(filepath.Join(testFolder, "test-dir", "sub-dir-1"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(testFolder, "test-dir", "sub-dir-2"), 0o755))
	filePath := filepath.Join(testFolder, "test-dir", "sub-dir-1", "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("Hello, World!"), 0o644))

	// Service instance
	svc := mockService()

	// Helper to inject user into context
	injectUser := func(ctx context.Context, u *user.User) context.Context {
		return authn.SetInfo(ctx, u)
	}

	tests := []struct {
		name          string
		depth         uint32
		expectedPaths []string
	}{
		{
			name:  "depth 0 lists only root directory",
			depth: 0,
			expectedPaths: []string{
				filepath.Join(testFolder, "test-dir"),
			},
		},
		{
			name:  "depth 1 lists root directory",
			depth: 1,
			expectedPaths: []string{
				filepath.Join(testFolder, "test-dir"),
			},
		},
		{
			name:  "depth 2 lists first level of subdirectories (in this case the root directory)",
			depth: 2,
			expectedPaths: []string{
				filepath.Join(testFolder, "test-dir"),
				filepath.Join(testFolder, "test-dir", "sub-dir-1"),
				filepath.Join(testFolder, "test-dir", "sub-dir-2"),
			},
		},
		{
			name:  "depth 3 lists all directories and files",
			depth: 3,
			expectedPaths: []string{
				filepath.Join(testFolder, "test-dir"),
				filepath.Join(testFolder, "test-dir", "sub-dir-1"),
				filepath.Join(testFolder, "test-dir", "sub-dir-2"),
				filepath.Join(testFolder, "test-dir", "sub-dir-1", "file.txt"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := injectUser(t.Context(), u)
			req := connect.NewRequest(&filesystem.ListDirRequest{
				Path:  testFolder,
				Depth: tt.depth,
			})
			resp, err := svc.ListDir(ctx, req)
			require.NoError(t, err)
			assert.NotEmpty(t, resp.Msg)
			assert.Len(t, resp.Msg.GetEntries(), len(tt.expectedPaths))
			actualPaths := make([]string, len(resp.Msg.GetEntries()))
			for i, entry := range resp.Msg.GetEntries() {
				actualPaths[i] = entry.GetPath()
			}
			assert.ElementsMatch(t, tt.expectedPaths, actualPaths)
		})
	}
}

func TestListDirNonExistingPath(t *testing.T) {
	t.Parallel()

	svc := mockService()
	u, err := user.Current()
	require.NoError(t, err)
	ctx := authn.SetInfo(t.Context(), u)

	req := connect.NewRequest(&filesystem.ListDirRequest{
		Path:  "/non-existing-path",
		Depth: 1,
	})
	_, err = svc.ListDir(ctx, req)
	require.Error(t, err)
	var connectErr *connect.Error
	ok := errors.As(err, &connectErr)
	assert.True(t, ok, "expected error to be of type *connect.Error")
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
}

func TestListDirRelativePath(t *testing.T) {
	t.Parallel()

	// Setup temp root and user
	u, err := user.Current()
	require.NoError(t, err)

	// Setup directory structure
	testRelativePath := fmt.Sprintf("test-%s", uuid.New())
	testFolderPath := filepath.Join(u.HomeDir, testRelativePath)
	filePath := filepath.Join(testFolderPath, "file.txt")
	require.NoError(t, os.MkdirAll(testFolderPath, 0o755))
	require.NoError(t, os.WriteFile(filePath, []byte("Hello, World!"), 0o644))

	// Service instance
	svc := mockService()
	ctx := authn.SetInfo(t.Context(), u)

	req := connect.NewRequest(&filesystem.ListDirRequest{
		Path:  testRelativePath,
		Depth: 1,
	})
	resp, err := svc.ListDir(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Msg)

	expectedPaths := []string{
		filepath.Join(testFolderPath, "file.txt"),
	}
	assert.Len(t, resp.Msg.GetEntries(), len(expectedPaths))

	actualPaths := make([]string, len(resp.Msg.GetEntries()))
	for i, entry := range resp.Msg.GetEntries() {
		actualPaths[i] = entry.GetPath()
	}
	assert.ElementsMatch(t, expectedPaths, actualPaths)
}

func TestListDir_Symlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	u, err := user.Current()
	require.NoError(t, err)
	ctx := authn.SetInfo(t.Context(), u)

	symlinkRoot := filepath.Join(root, "test-symlinks")
	require.NoError(t, os.MkdirAll(symlinkRoot, 0o755))

	// 1. Prepare a real directory + file that a symlink will point to
	realDir := filepath.Join(symlinkRoot, "real-dir")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	filePath := filepath.Join(realDir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello via symlink"), 0o644))

	// 2. Prepare a standalone real file (points-to-file scenario)
	realFile := filepath.Join(symlinkRoot, "real-file.txt")
	require.NoError(t, os.WriteFile(realFile, []byte("i am a plain file"), 0o644))

	// 3. Create the three symlinks
	linkToDir := filepath.Join(symlinkRoot, "link-dir")   // → directory
	linkToFile := filepath.Join(symlinkRoot, "link-file") // → file
	cyclicLink := filepath.Join(symlinkRoot, "cyclic")    // → itself
	require.NoError(t, os.Symlink(realDir, linkToDir))
	require.NoError(t, os.Symlink(realFile, linkToFile))
	require.NoError(t, os.Symlink(cyclicLink, cyclicLink))

	svc := mockService()

	t.Run("symlink to directory behaves like directory and the content looks like inside the directory", func(t *testing.T) {
		t.Parallel()

		req := connect.NewRequest(&filesystem.ListDirRequest{
			Path:  linkToDir,
			Depth: 1,
		})
		resp, err := svc.ListDir(ctx, req)
		require.NoError(t, err)
		expected := []string{
			filepath.Join(linkToDir, "file.txt"),
		}
		actual := make([]string, len(resp.Msg.GetEntries()))
		for i, e := range resp.Msg.GetEntries() {
			actual[i] = e.GetPath()
		}
		assert.ElementsMatch(t, expected, actual)
	})

	t.Run("link to file", func(t *testing.T) {
		t.Parallel()

		req := connect.NewRequest(&filesystem.ListDirRequest{
			Path:  linkToFile,
			Depth: 1,
		})
		_, err := svc.ListDir(ctx, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})

	t.Run("cyclic symlink surfaces 'too many links' → invalid-argument", func(t *testing.T) {
		t.Parallel()

		req := connect.NewRequest(&filesystem.ListDirRequest{
			Path: cyclicLink,
		})
		_, err := svc.ListDir(ctx, req)
		require.Error(t, err)
		var connectErr *connect.Error
		ok := errors.As(err, &connectErr)
		assert.True(t, ok, "expected error to be of type *connect.Error")
		assert.Equal(t, connect.CodeFailedPrecondition, connectErr.Code())
		assert.Contains(t, connectErr.Error(), "cyclic symlink")
	})

	t.Run("symlink not resolved if not root", func(t *testing.T) {
		t.Parallel()

		req := connect.NewRequest(&filesystem.ListDirRequest{
			Path:  symlinkRoot,
			Depth: 3,
		})
		res, err := svc.ListDir(ctx, req)
		require.NoError(t, err)
		expected := []string{
			filepath.Join(symlinkRoot, "cyclic"),
			filepath.Join(symlinkRoot, "link-dir"),
			filepath.Join(symlinkRoot, "link-file"),
			filepath.Join(symlinkRoot, "real-dir"),
			filepath.Join(symlinkRoot, "real-dir", "file.txt"),
			filepath.Join(symlinkRoot, "real-file.txt"),
		}
		actual := make([]string, len(res.Msg.GetEntries()))
		for i, e := range res.Msg.GetEntries() {
			actual[i] = e.GetPath()
		}
		assert.ElementsMatch(t, expected, actual, "symlinks should not be resolved when listing the symlink root directory")
	})
}

// TestFollowSymlink_Success makes sure that followSymlink resolves symlinks,
// while also being robust to the /var → /private/var indirection that exists on macOS.
func TestFollowSymlink_Success(t *testing.T) {
	t.Parallel()

	// Base temporary directory. On macOS this lives under /var/folders/…
	// which itself is a symlink to /private/var/folders/….
	base := t.TempDir()

	// Create a real directory that we ultimately want to resolve to.
	target := filepath.Join(base, "target")
	require.NoError(t, os.MkdirAll(target, 0o755))

	// Create a symlink pointing at the real directory so we can verify that
	// followSymlink follows it.
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(target, link))

	got, err := followSymlink(link)
	require.NoError(t, err)

	// Canonicalise the expected path too, so that /var → /private/var (macOS)
	// or any other benign symlink indirections don’t cause flaky tests.
	want, err := filepath.EvalSymlinks(link)
	require.NoError(t, err)

	require.Equal(t, want, got, "followSymlink should resolve and canonicalise symlinks")
}

// TestFollowSymlink_MultiSymlinkChain verifies that followSymlink follows a chain
// of several symlinks (non‑cyclic) correctly.
func TestFollowSymlink_MultiSymlinkChain(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	// Final destination directory.
	target := filepath.Join(base, "target")
	require.NoError(t, os.MkdirAll(target, 0o755))

	// Build a 3‑link chain: link1 → link2 → link3 → target.
	link3 := filepath.Join(base, "link3")
	require.NoError(t, os.Symlink(target, link3))

	link2 := filepath.Join(base, "link2")
	require.NoError(t, os.Symlink(link3, link2))

	link1 := filepath.Join(base, "link1")
	require.NoError(t, os.Symlink(link2, link1))

	got, err := followSymlink(link1)
	require.NoError(t, err)

	want, err := filepath.EvalSymlinks(link1)
	require.NoError(t, err)

	require.Equal(t, want, got, "followSymlink should resolve an arbitrary symlink chain")
}

func TestFollowSymlink_NotFound(t *testing.T) {
	t.Parallel()

	_, err := followSymlink("/definitely/does/not/exist")
	require.Error(t, err)

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, connect.CodeNotFound, cerr.Code())
}

func TestFollowSymlink_CyclicSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	require.NoError(t, os.MkdirAll(a, 0o755))
	require.NoError(t, os.MkdirAll(b, 0o755))

	// Create a two‑node loop: a/loop → b/loop, b/loop → a/loop.
	require.NoError(t, os.Symlink(filepath.Join(b, "loop"), filepath.Join(a, "loop")))
	require.NoError(t, os.Symlink(filepath.Join(a, "loop"), filepath.Join(b, "loop")))

	_, err := followSymlink(filepath.Join(a, "loop"))
	require.Error(t, err)

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, connect.CodeFailedPrecondition, cerr.Code())
	require.Contains(t, cerr.Message(), "cyclic")
}

func TestCheckIfDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, checkIfDirectory(dir))

	file := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o644))

	err := checkIfDirectory(file)
	require.Error(t, err)

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, connect.CodeInvalidArgument, cerr.Code())
}

func TestWalkDir_Depth(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	subsub := filepath.Join(sub, "subsub")
	require.NoError(t, os.MkdirAll(subsub, 0o755))

	entries, err := walkDir(root, root, 1)
	require.NoError(t, err)

	// Collect the names for easier assertions.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.GetName())
	}

	require.Contains(t, names, "sub")
	require.NotContains(t, names, "subsub", "entries beyond depth should be excluded")
}

func TestWalkDir_Error(t *testing.T) {
	t.Parallel()

	_, err := walkDir("/does/not/exist", "/does/not/exist", 1)
	require.Error(t, err)

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, connect.CodeInternal, cerr.Code())
}
