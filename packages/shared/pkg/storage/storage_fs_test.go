package storage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// helper to create a FileSystem backend rooted in a temp directory.
func newTempProvider(t *testing.T) (*Backend, string) {
	t.Helper()

	base := t.TempDir()
	p := NewFS(base)

	return p, base
}

func ensureParentDir(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
}

func TestOpenObject_Upload_Size_Download(t *testing.T) {
	t.Parallel()
	p, base := newTempProvider(t)
	ctx := t.Context()

	testPath := filepath.Join(base, "sub", "file.txt")
	contents := []byte("hello world")
	ensureParentDir(t, testPath)

	// write via Upload
	_, err := p.Upload(ctx, testPath, bytes.NewReader(contents))
	require.NoError(t, err)

	// check Size
	_, size, err := p.Size(ctx, testPath)
	require.NoError(t, err)
	require.Equal(t, int64(len(contents)), size)

	// read the entire file back via Get
	var buf bytes.Buffer
	r, err := p.StartDownload(ctx, testPath)
	require.NoError(t, err)
	defer r.Close()

	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	require.Equal(t, contents, buf.Bytes())
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
	data, err := io.ReadAll(src)
	require.NoError(t, err)
	_, err = p.Upload(ctx, dstPath, bytes.NewReader(data))
	require.NoError(t, err)

	var buf bytes.Buffer
	r, err := p.StartDownload(ctx, dstPath)
	require.NoError(t, err)
	defer r.Close()
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	require.Equal(t, payload, buf.String())
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
		_, err := p.Upload(ctx, fullPath, bytes.NewReader([]byte("x")))
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
	_, err := p.StartDownload(ctx, missingPath)
	require.ErrorIs(t, err, ErrObjectNotExist)
}
