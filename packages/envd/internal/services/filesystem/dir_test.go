package filesystem

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

// TestResolvePath_Success makes sure that resolvePath expands the user path
// (via permissions.ExpandAndResolve) **and** resolves symlinks, while also
// being robust to the /var → /private/var indirection that exists on macOS.
func TestResolvePath_Success(t *testing.T) {
	t.Parallel()

	// Base temporary directory. On macOS this lives under /var/folders/…
	// which itself is a symlink to /private/var/folders/….
	base := t.TempDir()

	// Create a real directory that we ultimately want to resolve to.
	target := filepath.Join(base, "target")
	require.NoError(t, os.MkdirAll(target, 0o755))

	// Create a symlink pointing at the real directory so we can verify that
	// resolvePath follows it.
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(target, link))

	// Current user (needed by resolvePath signature).
	u, err := user.Current()
	require.NoError(t, err)

	got, err := resolvePath(link, u)
	require.NoError(t, err)

	// Canonicalise the expected path too, so that /var → /private/var (macOS)
	// or any other benign symlink indirections don’t cause flaky tests.
	want, err := filepath.EvalSymlinks(link)
	require.NoError(t, err)

	require.Equal(t, want, got, "resolvePath should resolve and canonicalise symlinks")
}

// TestResolvePath_MultiSymlinkChain verifies that resolvePath follows a chain
// of several symlinks (non‑cyclic) correctly.
func TestResolvePath_MultiSymlinkChain(t *testing.T) {
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

	u, err := user.Current()
	require.NoError(t, err)

	got, err := resolvePath(link1, u)
	require.NoError(t, err)

	want, err := filepath.EvalSymlinks(link1)
	require.NoError(t, err)

	require.Equal(t, want, got, "resolvePath should resolve an arbitrary symlink chain")
}

func TestResolvePath_NotFound(t *testing.T) {
	t.Parallel()

	u, _ := user.Current()
	_, err := resolvePath("/definitely/does/not/exist", u)
	require.Error(t, err)

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, connect.CodeNotFound, cerr.Code())
}

func TestResolvePath_CyclicSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	require.NoError(t, os.MkdirAll(a, 0o755))
	require.NoError(t, os.MkdirAll(b, 0o755))

	// Create a two‑node loop: a/loop → b/loop, b/loop → a/loop.
	require.NoError(t, os.Symlink(filepath.Join(b, "loop"), filepath.Join(a, "loop")))
	require.NoError(t, os.Symlink(filepath.Join(a, "loop"), filepath.Join(b, "loop")))

	u, _ := user.Current()
	_, err := resolvePath(filepath.Join(a, "loop"), u)
	require.Error(t, err)

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, connect.CodeInvalidArgument, cerr.Code())
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

	entries, err := walkDir(root, 1)
	require.NoError(t, err)

	// Collect the names for easier assertions.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}

	require.Contains(t, names, "sub")
	require.NotContains(t, names, "subsub", "entries beyond depth should be excluded")
}

func TestWalkDir_Error(t *testing.T) {
	t.Parallel()

	_, err := walkDir("/does/not/exist", 1)
	require.Error(t, err)

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	require.Equal(t, connect.CodeInternal, cerr.Code())
}
