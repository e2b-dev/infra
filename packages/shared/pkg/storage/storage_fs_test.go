package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// helper to create a FileSystemStorageProvider rooted in a temp directory.
func newTempProvider(t *testing.T) *FileSystemStorageProvider {
	t.Helper()

	base := t.TempDir()
	p, err := NewFileSystemStorageProvider(base)
	require.NoError(t, err)
	return p
}

func TestOpenObject_ReadWrite_Size_ReadAt(t *testing.T) {
	p := newTempProvider(t)
	ctx := context.Background()

	obj, err := p.OpenObject(ctx, filepath.Join("sub", "file.txt"))
	require.NoError(t, err)

	contents := []byte("hello world")
	// write via Write
	n, err := obj.Write(contents)
	require.NoError(t, err)
	require.Equal(t, len(contents), n)

	// check Size
	size, err := obj.Size()
	require.NoError(t, err)
	require.Equal(t, int64(len(contents)), size)

	// read the entire file back via WriteTo
	var buf bytes.Buffer
	n64, err := obj.WriteTo(&buf)
	require.NoError(t, err)
	require.Equal(t, int64(len(contents)), n64)
	require.Equal(t, contents, buf.Bytes())

	// read a slice via ReadAt ("world")
	part := make([]byte, 5)
	nRead, err := obj.ReadAt(part, 6)
	require.NoError(t, err)
	require.Equal(t, 5, nRead)
	require.Equal(t, []byte("world"), part)
}

func TestWriteFromFileSystem(t *testing.T) {
	p := newTempProvider(t)
	ctx := context.Background()

	// create a separate source file on disk
	srcPath := filepath.Join(t.TempDir(), "src.txt")
	const payload = "copy me please"
	require.NoError(t, os.WriteFile(srcPath, []byte(payload), 0o600))

	obj, err := p.OpenObject(ctx, "copy/dst.txt")
	require.NoError(t, err)
	require.NoError(t, obj.WriteFromFileSystem(srcPath))

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	require.NoError(t, err)
	require.Equal(t, payload, buf.String())
}

func TestDelete(t *testing.T) {
	p := newTempProvider(t)
	ctx := context.Background()

	obj, err := p.OpenObject(ctx, "to/delete.txt")
	require.NoError(t, err)

	_, err = obj.Write([]byte("bye"))
	require.NoError(t, err)
	require.NoError(t, obj.Delete())

	// subsequent Size call should fail with ErrObjectNotExist
	_, err = obj.Size()
	require.ErrorIs(t, err, ErrObjectNotExist)
}

func TestDeleteObjectsWithPrefix(t *testing.T) {
	p := newTempProvider(t)
	ctx := context.Background()

	paths := []string{
		"data/a.txt",
		"data/b.txt",
		"data/sub/c.txt",
	}
	for _, pth := range paths {
		obj, err := p.OpenObject(ctx, pth)
		require.NoError(t, err)
		_, err = obj.Write([]byte("x"))
		require.NoError(t, err)
	}

	// remove the entire "data" prefix
	require.NoError(t, p.DeleteObjectsWithPrefix(ctx, "data"))

	for _, pth := range paths {
		full := filepath.Join(p.GetDetails()[len("[Local file storage, base path set to "):len(p.GetDetails())-1], pth) // derive basePath
		_, err := os.Stat(full)
		require.True(t, os.IsNotExist(err))
	}
}

func TestWriteToNonExistentObject(t *testing.T) {
	p := newTempProvider(t)

	ctx := context.Background()
	obj, err := p.OpenObject(ctx, "missing/file.txt")
	require.NoError(t, err)

	var sink bytes.Buffer
	_, err = obj.WriteTo(&sink)
	require.ErrorIs(t, err, ErrObjectNotExist)
}
