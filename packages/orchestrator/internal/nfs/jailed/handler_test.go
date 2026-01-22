package jailed

import (
	"context"
	"net"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"
)

func TestJailedFS(t *testing.T) {
	t.Parallel()

	fs := memfs.New()

	// Create a "good" folder and a "bad" file outside of it
	err := fs.MkdirAll("good_folder", 0o755)
	require.NoError(t, err)

	f, err := fs.Create("bad_file")
	require.NoError(t, err)
	_, err = f.Write([]byte("bad content"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)

	f, err = fs.Create("good_folder/good_file")
	require.NoError(t, err)
	_, err = f.Write([]byte("good content"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)

	// Setup jailed handler
	getPrefix := func(_ context.Context, _ net.Conn, _ nfs.MountRequest) (string, error) {
		return "good_folder", nil
	}

	innerHandler := helpers.NewNullAuthHandler(fs)
	handler := NewNFSHandler(innerHandler, getPrefix)

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
		path := jfs.Join("../bad_file")
		_, err := jfs.Open(path)
		require.ErrorIs(t, err, billy.ErrCrossedBoundary)
	})
}
