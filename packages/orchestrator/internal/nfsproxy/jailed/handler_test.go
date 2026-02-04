package jailed

import (
	"context"
	"io"
	"net"
	"os"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/oschange"
)

func mkdir(t *testing.T, fs billy.Filesystem, path string, perm os.FileMode) {
	t.Helper()

	err := fs.MkdirAll(path, perm)
	require.NoError(t, err)
}

func write(t *testing.T, fs billy.Filesystem, path string, perm os.FileMode, content string) {
	t.Helper()

	f, err := fs.OpenFile(path, os.O_CREATE|os.O_WRONLY, perm)
	require.NoError(t, err)
	_, err = f.Write([]byte(content))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)
}

func TestJailedFS(t *testing.T) {
	t.Parallel()

	fs := memfs.New()

	// create file system
	write(t, fs, "bad_file", 0o644, "bad content")
	mkdir(t, fs, "good_folder", 0o755)
	write(t, fs, "good_folder/bad_file", 0o644, "okay content")
	write(t, fs, "good_folder/good_file", 0o644, "good content")
	mkdir(t, fs, "good_folder/more_dir", 0o755)
	write(t, fs, "good_folder/more_dir/other_file", 0o644, "more content")

	// Setup jailed handler
	getPrefix := func(_ context.Context, _ net.Conn, _ nfs.MountRequest) (billy.Filesystem, string, error) {
		return fs, "good_folder", nil
	}

	handler := NewNFSHandler(getPrefix, func(_ billy.Filesystem) billy.Change {
		return oschange.NewChange("")
	})

	// Simulate mount
	ctx := context.Background()
	status, jfs, _ := handler.Mount(ctx, nil, nfs.MountRequest{Dirpath: []byte("/")})
	require.Equal(t, nfs.MountStatusOk, status)

	t.Run("access good file", func(t *testing.T) {
		t.Parallel()

		path := jfs.Join("good_file")
		f, err := jfs.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() {
			err := f.Close()
			assert.NoError(t, err)
		})

		buf := make([]byte, 12)
		n, err := f.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "good content", string(buf[:n]))
	})

	t.Run("access bad file via traversal", func(t *testing.T) {
		t.Parallel()

		// This should fail if jailed
		path := jfs.Join("../../bad_file")
		_, err := jfs.Open(path)
		require.ErrorIs(t, err, billy.ErrCrossedBoundary)
	})

	t.Run("access good file via traversal", func(t *testing.T) {
		t.Parallel()

		// This should succeed if jailed
		path := jfs.Join("more_dir/../more_dir/other_file")
		fp, err := jfs.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() {
			err := fp.Close()
			assert.NoError(t, err)
		})
		data, err := io.ReadAll(fp)
		require.NoError(t, err)
		assert.Equal(t, "more content", string(data))
	})
}
