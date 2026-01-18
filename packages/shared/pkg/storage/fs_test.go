package storage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper to create a FileSystem Provider rooted in a temp directory.
func newTempProvider(t *testing.T) (*Provider, string) {
	t.Helper()

	base := t.TempDir()
	p := NewFS(base)

	return p, base
}

func ensureParentDir(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
}

func TestOpenObject_Write_Exists_WriteTo(t *testing.T) {
	t.Parallel()
	p, base := newTempProvider(t)
	ctx := t.Context()

	testPath := filepath.Join(base, "sub", "file.txt")
	contents := []byte("hello world")
	ensureParentDir(t, testPath)

	// write via Put
	err := p.Put(ctx, testPath, bytes.NewReader(contents))
	require.NoError(t, err)

	// check Exists
	exists, err := p.Exists(ctx, testPath)
	require.NoError(t, err)
	require.True(t, exists)

	// read the entire file back via Get
	reader, err := p.Get(ctx, testPath)
	require.NoError(t, err)
	defer reader.Close()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, contents, data)
}

func TestFSPut(t *testing.T) {
	t.Parallel()
	p, base := newTempProvider(t)
	ctx := t.Context()

	// create a separate source file on disk
	srcPath := filepath.Join(t.TempDir(), "src.txt")
	const payload = "copy me please"
	require.NoError(t, os.WriteFile(srcPath, []byte(payload), 0o600))

	src, err := os.Open(srcPath)
	require.NoError(t, err)
	defer src.Close()

	dstPath := filepath.Join(base, "copy", "dst.txt")
	ensureParentDir(t, dstPath)
	require.NoError(t, p.Put(ctx, dstPath, src))

	reader, err := p.Get(ctx, dstPath)
	require.NoError(t, err)
	defer reader.Close()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, payload, string(data))
}

func TestDelete(t *testing.T) {
	t.Parallel()
	p, base := newTempProvider(t)
	ctx := t.Context()

	path := filepath.Join(base, "to", "delete.txt")
	ensureParentDir(t, path)

	err := p.Put(ctx, path, bytes.NewReader([]byte("bye")))
	require.NoError(t, err)

	exists, err := p.Exists(ctx, path)
	require.NoError(t, err)
	assert.True(t, exists)

	err = p.DeleteWithPrefix(ctx, filepath.Join("to", "delete.txt"))
	require.NoError(t, err)

	// subsequent Exists call should return false
	exists, err = p.Exists(ctx, path)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestDeleteObjectsWithPrefix(t *testing.T) {
	t.Parallel()
	p, base := newTempProvider(t)
	ctx := t.Context()

	paths := []string{
		"data/a.txt",
		"data/b.txt",
		"data/sub/c.txt",
	}
	for _, pth := range paths {
		fullPath := filepath.Join(base, pth)
		ensureParentDir(t, fullPath)
		err := p.Put(ctx, fullPath, bytes.NewReader([]byte("x")))
		require.NoError(t, err)
	}

	// remove the entire "data" prefix
	require.NoError(t, p.DeleteWithPrefix(ctx, "data"))

	for _, pth := range paths {
		full := filepath.Join(base, pth)
		_, err := os.Stat(full)
		require.True(t, os.IsNotExist(err))
	}
}

func TestWriteToNonExistentObject(t *testing.T) {
	t.Parallel()
	p, base := newTempProvider(t)

	ctx := t.Context()
	missingPath := filepath.Join(base, "missing", "file.txt")
	reader, err := p.Get(ctx, missingPath)
	if err == nil {
		reader.Close()
	}
	require.ErrorIs(t, err, ErrObjectNotExist)
}
