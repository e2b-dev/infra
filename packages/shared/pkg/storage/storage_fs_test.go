package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper to create a FileSystemStorageProvider rooted in a temp directory.
func newTempProvider(t *testing.T) *fsStore {
	t.Helper()

	base := t.TempDir()
	p, err := newFSStore(base)
	require.NoError(t, err)

	return p
}

func TestOpenObject_Write_Exists_WriteTo(t *testing.T) {
	p := newTempProvider(t)
	ctx := t.Context()

	obj, err := p.OpenObject(ctx, filepath.Join("sub", "file.txt"), MetadataObjectType)
	require.NoError(t, err)

	contents := []byte("hello world")
	// write via Write
	n, err := obj.Write(t.Context(), contents)
	require.NoError(t, err)
	require.Equal(t, len(contents), n)

	// check Size
	exists, err := obj.Exists(t.Context())
	require.NoError(t, err)
	require.True(t, exists)

	// read the entire file back via WriteTo
	var buf bytes.Buffer
	n64, err := obj.WriteTo(t.Context(), &buf)
	require.NoError(t, err)
	require.Equal(t, int64(len(contents)), n64)
	require.Equal(t, contents, buf.Bytes())
}

func TestWriteFromFileSystem(t *testing.T) {
	p := newTempProvider(t)
	ctx := t.Context()

	// create a separate source file on disk
	srcPath := filepath.Join(t.TempDir(), "src.txt")
	const payload = "copy me please"
	require.NoError(t, os.WriteFile(srcPath, []byte(payload), 0o600))

	obj, err := p.OpenObject(ctx, "copy/dst.txt", UnknownObjectType)
	require.NoError(t, err)
	err = obj.CopyFromFileSystem(t.Context(), srcPath)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = obj.WriteTo(t.Context(), &buf)
	require.NoError(t, err)
	require.Equal(t, payload, buf.String())
}

func TestDelete(t *testing.T) {
	p := newTempProvider(t)
	ctx := t.Context()

	obj, err := p.OpenObject(ctx, "to/delete.txt", UnknownObjectType)
	require.NoError(t, err)

	_, err = obj.Write(t.Context(), []byte("bye"))
	require.NoError(t, err)

	exists, err := obj.Exists(t.Context())
	require.NoError(t, err)
	assert.True(t, exists)

	err = p.DeleteObjectsWithPrefix(t.Context(), "to/delete.txt")
	require.NoError(t, err)

	// subsequent Size call should fail with ErrorObjectNotExist
	exists, err = obj.Exists(t.Context())
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestDeleteObjectsWithPrefix(t *testing.T) {
	p := newTempProvider(t)
	ctx := t.Context()

	paths := []string{
		"data/a.txt",
		"data/b.txt",
		"data/sub/c.txt",
	}
	for _, pth := range paths {
		obj, err := p.OpenObject(ctx, pth, UnknownObjectType)
		require.NoError(t, err)
		_, err = obj.Write(t.Context(), []byte("x"))
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

	ctx := t.Context()
	obj, err := p.OpenObject(ctx, "missing/file.txt", UnknownObjectType)
	require.NoError(t, err)

	var sink bytes.Buffer
	_, err = obj.WriteTo(t.Context(), &sink)
	require.ErrorIs(t, err, ErrObjectNotExist)
}
