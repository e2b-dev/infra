package recovery

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	nfs "github.com/willscott/go-nfs"

	nfsproxymocks "github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/mocks"
)

// ---- Tests: file.go ----

func TestFile_Write_PanicRecovered(t *testing.T) {
	t.Parallel()
	mf := nfsproxymocks.NewMockFile(t)
	mf.EXPECT().Write(mock.Anything).
		RunAndReturn(func(_ []byte) (int, error) { panic("File.Write") })
	f := wrapFile(context.Background(), mf)
	n, err := f.Write([]byte("abc"))
	require.ErrorIs(t, err, ErrPanic)
	require.Equal(t, 0, n)
}

func TestFile_Truncate_Happy(t *testing.T) {
	t.Parallel()
	mf := nfsproxymocks.NewMockFile(t)
	mf.EXPECT().Truncate(int64(0)).Return(nil)
	f := wrapFile(context.Background(), mf)
	require.NoError(t, f.Truncate(0))
}

func TestFile_Name_Panic_NoCrash(t *testing.T) {
	t.Parallel()
	mf := nfsproxymocks.NewMockFile(t)
	mf.EXPECT().Name().RunAndReturn(func() string { panic("File.Name") })
	f := wrapFile(context.Background(), mf)
	// Should not panic; should return zero value
	got := f.Name()
	require.Empty(t, got)
}

func TestFile_Write_Error_Propagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	mf := nfsproxymocks.NewMockFile(t)
	mf.EXPECT().Write(mock.Anything).Return(0, boom)
	f := wrapFile(context.Background(), mf)
	_, err := f.Write([]byte("x"))
	require.ErrorIs(t, err, boom)
}

// ---- Tests: fs.go ----

func TestFS_Stat_PanicRecovered(t *testing.T) {
	t.Parallel()
	mfs := nfsproxymocks.NewMockFilesystem(t)
	mfs.EXPECT().Stat("/x").RunAndReturn(func(string) (os.FileInfo, error) { panic("FS.Stat") })

	fs := wrapFS(context.Background(), mfs)
	_, err := fs.Stat("/x")
	require.ErrorIs(t, err, ErrPanic)
}

func TestFS_Create_Happy_WrapsFile(t *testing.T) {
	t.Parallel()
	mfs := nfsproxymocks.NewMockFilesystem(t)
	mf := nfsproxymocks.NewMockFile(t)
	mfs.EXPECT().Create("/file.txt").Return(mf, nil)
	fs := wrapFS(context.Background(), mfs)
	f, err := fs.Create("/file.txt")
	require.NoError(t, err)
	// ensure the returned file is our wrapped type
	require.IsType(t, &file{}, f)
}

func TestFS_Join_Panic_NoCrash(t *testing.T) {
	t.Parallel()
	mfs := nfsproxymocks.NewMockFilesystem(t)
	// The generated mock treats variadic args as a single []string parameter in expectation.
	mfs.EXPECT().Join([]string{"a", "b"}).
		RunAndReturn(func(_ ...string) string { panic("Join") })
	fs := wrapFS(context.Background(), mfs)
	require.NotPanics(t, func() { _ = fs.Join("a", "b") }) // should not panic
}

func TestFS_Remove_Error_Propagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	mfs := nfsproxymocks.NewMockFilesystem(t)
	mfs.EXPECT().Remove("/x").Return(boom)
	fs := wrapFS(context.Background(), mfs)
	err := fs.Remove("/x")
	require.ErrorIs(t, err, boom)
}

// ---- Tests: change.go ----

func TestChange_Chmod_PanicRecovered(t *testing.T) {
	t.Parallel()
	mch := nfsproxymocks.NewMockChange(t)
	mch.EXPECT().Chmod("/x", os.FileMode(0o644)).
		RunAndReturn(func(string, os.FileMode) error { panic("Change.Chmod") })
	ch := wrapChange(context.Background(), mch)
	require.ErrorIs(t, ch.Chmod("/x", 0o644), ErrPanic)
}

func TestChange_Chown_Happy(t *testing.T) {
	t.Parallel()
	mch := nfsproxymocks.NewMockChange(t)
	mch.EXPECT().Chown("/x", 1, 1).Return(nil)
	ch := wrapChange(context.Background(), mch)
	require.NoError(t, ch.Chown("/x", 1, 1))
}

func TestChange_Chtimes_Error_Propagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	mch := nfsproxymocks.NewMockChange(t)
	mch.EXPECT().Chtimes("/x", mock.Anything, mock.Anything).Return(boom)
	ch := wrapChange(context.Background(), mch)
	err := ch.Chtimes("/x", time.Unix(0, 0), time.Unix(0, 0))
	require.ErrorIs(t, err, boom)
}

// ---- Tests: main.go (Handler) ----

func TestHandler_FSStat_PanicRecovered(t *testing.T) {
	t.Parallel()
	mh := nfsproxymocks.NewMockHandler(t)
	mh.EXPECT().FSStat(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, billy.Filesystem, *nfs.FSStat) error { panic("Handler.FSStat") })
	h := WrapWithRecovery(context.Background(), mh)
	var stat nfs.FSStat
	require.ErrorIs(t, h.FSStat(context.Background(), nil, &stat), ErrPanic)
}

func TestHandler_Mount_Panic_NoCrash(t *testing.T) {
	t.Parallel()
	mh := nfsproxymocks.NewMockHandler(t)
	mh.EXPECT().Mount(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, net.Conn, nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
			panic("Mount")
		})
	h := WrapWithRecovery(context.Background(), mh)
	status, fs, auth := h.Mount(context.Background(), nil, nfs.MountRequest{})
	// On panic, zero values returned. Ensure it didn't panic and fs is nil.
	require.Nil(t, fs)
	require.Zero(t, status)
	require.Empty(t, auth)
}

func TestHandler_Mount_WrapsFS(t *testing.T) {
	t.Parallel()
	base := nfsproxymocks.NewMockFilesystem(t)
	mh := nfsproxymocks.NewMockHandler(t)
	mh.EXPECT().Mount(mock.Anything, mock.Anything, mock.Anything).Return(nfs.MountStatus(0), base, nil)
	h := WrapWithRecovery(context.Background(), mh)
	_, fs, _ := h.Mount(context.Background(), nil, nfs.MountRequest{})
	require.IsType(t, &filesystem{}, fs)
}

func TestHandler_FromHandle_PanicRecovered(t *testing.T) {
	t.Parallel()
	mh := nfsproxymocks.NewMockHandler(t)
	mh.EXPECT().FromHandle(mock.Anything).
		RunAndReturn(func([]byte) (billy.Filesystem, []string, error) { panic("Handler.FromHandle") })
	h := WrapWithRecovery(context.Background(), mh)
	_, _, err := h.FromHandle([]byte("x"))
	require.ErrorIs(t, err, ErrPanic)
}

func TestHandler_InvalidateHandle_PanicRecovered(t *testing.T) {
	t.Parallel()
	mh := nfsproxymocks.NewMockHandler(t)
	mh.EXPECT().InvalidateHandle(mock.Anything, mock.Anything).
		RunAndReturn(func(billy.Filesystem, []byte) error { panic("Handler.InvalidateHandle") })
	h := WrapWithRecovery(context.Background(), mh)
	require.ErrorIs(t, h.InvalidateHandle(nil, []byte("x")), ErrPanic)
}

func TestHandler_Error_Propagation(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	mh := nfsproxymocks.NewMockHandler(t)
	// FSStat
	mh.EXPECT().FSStat(mock.Anything, mock.Anything, mock.Anything).Return(boom)
	h := WrapWithRecovery(context.Background(), mh)
	var stat nfs.FSStat
	err := h.FSStat(context.Background(), nil, &stat)
	require.ErrorIs(t, err, boom)

	// FromHandle
	mh2 := nfsproxymocks.NewMockHandler(t)
	mh2.EXPECT().FromHandle(mock.Anything).Return(billy.Filesystem(nil), nil, boom)
	h2 := WrapWithRecovery(context.Background(), mh2)
	_, _, err = h2.FromHandle([]byte("x"))
	require.ErrorIs(t, err, boom)

	// InvalidateHandle
	mh3 := nfsproxymocks.NewMockHandler(t)
	mh3.EXPECT().InvalidateHandle(mock.Anything, mock.Anything).Return(boom)
	h3 := WrapWithRecovery(context.Background(), mh3)
	err = h3.InvalidateHandle(nil, nil)
	require.ErrorIs(t, err, boom)
}
