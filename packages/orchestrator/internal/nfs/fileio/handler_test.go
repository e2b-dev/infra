package fileio

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs"
)

func TestNFSHandler_Mount(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	handler := NewNFSHandler(tmpDir)

	req := nfs.MountRequest{
		Dirpath: []byte("/test/subdir"),
	}

	status, fs, _ := handler.Mount(context.Background(), nil, req)

	require.Equal(t, nfs.MountStatusOk, status)
	require.NotNil(t, fs)

	// The mount should have created the subdirectory
	_, err := fs.Stat("test/subdir")
	// Note: The exact path may vary depending on how resolution works
	// The key thing is the mount succeeded
	assert.NoError(t, err)
}

func TestNFSHandler_Mount_RootPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	handler := NewNFSHandler(tmpDir)

	req := nfs.MountRequest{
		Dirpath: []byte("/"),
	}

	status, fs, _ := handler.Mount(context.Background(), nil, req)

	require.Equal(t, nfs.MountStatusOk, status)
	require.NotNil(t, fs)
}

func TestNFSHandler_Mount_EmptyPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	handler := NewNFSHandler(tmpDir)

	req := nfs.MountRequest{
		Dirpath: []byte(""),
	}

	status, fs, _ := handler.Mount(context.Background(), nil, req)

	require.Equal(t, nfs.MountStatusOk, status)
	require.NotNil(t, fs)
}

func TestNFSHandler_Change(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	handler := NewNFSHandler(tmpDir)

	fs := NewLocalFS(tmpDir)
	change := handler.Change(fs)

	require.NotNil(t, change)
}

func TestNFSHandler_String(t *testing.T) {
	t.Parallel()

	handler := NewNFSHandler("/test/path")
	s := handler.String()

	assert.Contains(t, s, "NFSHandler")
	assert.Contains(t, s, "/test/path")
}
