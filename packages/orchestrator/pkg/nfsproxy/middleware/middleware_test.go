package middleware_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
	nfsproxymocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/mocks"
)

var errTest = errors.New("test error")

// TestChain_ExecutesInterceptorsInOrder verifies that interceptors are executed
// in the order they were added to the chain.
func TestChain_ExecutesInterceptorsInOrder(t *testing.T) {
	t.Parallel()

	var order []int

	interceptor1 := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		order = append(order, 1)
		err := next(ctx)
		order = append(order, -1)

		return err
	}

	interceptor2 := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		order = append(order, 2)
		err := next(ctx)
		order = append(order, -2)

		return err
	}

	chain := middleware.NewChain(interceptor1, interceptor2)

	err := chain.Exec(context.Background(), "test.op", nil, func(ctx context.Context) error {
		order = append(order, 0)

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 0, -2, -1}, order)
}

// TestChain_PropagatesErrors verifies that errors from the inner function
// are propagated through all interceptors.
func TestChain_PropagatesErrors(t *testing.T) {
	t.Parallel()

	var interceptorSawError bool

	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		err := next(ctx)
		interceptorSawError = err != nil

		return err
	}

	chain := middleware.NewChain(interceptor)

	err := chain.Exec(context.Background(), "test.op", nil, func(ctx context.Context) error {
		return errTest
	})

	require.ErrorIs(t, err, errTest)
	assert.True(t, interceptorSawError)
}

// TestChain_InterceptorCanModifyError verifies that an interceptor can
// modify or wrap the error returned by the inner function.
func TestChain_InterceptorCanModifyError(t *testing.T) {
	t.Parallel()

	wrappedErr := errors.New("wrapped error")

	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		err := next(ctx)
		if err != nil {
			return wrappedErr
		}

		return nil
	}

	chain := middleware.NewChain(interceptor)

	err := chain.Exec(context.Background(), "test.op", nil, func(ctx context.Context) error {
		return errTest
	})

	require.ErrorIs(t, err, wrappedErr)
}

// TestChain_PassesOpAndArgs verifies that the operation name and arguments
// are correctly passed to interceptors.
func TestChain_PassesOpAndArgs(t *testing.T) {
	t.Parallel()

	var capturedOp string
	var capturedArgs []any

	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		capturedOp = op
		capturedArgs = args

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	args := []any{"arg1", 42, true}

	err := chain.Exec(context.Background(), "File.Read", args, func(ctx context.Context) error {
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, "File.Read", capturedOp)
	assert.Equal(t, args, capturedArgs)
}

// TestChain_EmptyChain verifies that an empty chain just executes the function.
func TestChain_EmptyChain(t *testing.T) {
	t.Parallel()

	chain := middleware.NewChain()
	called := false

	err := chain.Exec(context.Background(), "test.op", nil, func(ctx context.Context) error {
		called = true

		return nil
	})

	require.NoError(t, err)
	assert.True(t, called)
}

// TestWrapFile_ReturnsNilForNilInput verifies that WrapFile returns nil
// when given a nil file.
func TestWrapFile_ReturnsNilForNilInput(t *testing.T) {
	t.Parallel()

	chain := middleware.NewChain()
	result := middleware.WrapFile(context.Background(), nil, chain)

	assert.Nil(t, result)
}

// TestWrappedFile_Write verifies that Write calls the inner file and
// executes interceptors.
func TestWrappedFile_Write(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Write([]byte("hello")).Return(5, nil)

	var interceptorCalled bool
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		interceptorCalled = true
		assert.Equal(t, "File.Write", op)
		assert.Len(t, args, 1)

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	n, err := wrapped.Write([]byte("hello"))

	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.True(t, interceptorCalled)
}

// TestWrappedFile_Write_WithError verifies that Write returns both
// the bytes written and the error.
func TestWrappedFile_Write_WithError(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Write([]byte("hello")).Return(3, errTest)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	n, err := wrapped.Write([]byte("hello"))

	require.ErrorIs(t, err, errTest)
	assert.Equal(t, 3, n)
}

// TestWrappedFile_Read verifies that Read calls the inner file and
// returns the correct values.
func TestWrappedFile_Read(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Read(mock.Anything).Run(func(p []byte) {
		copy(p, "hello")
	}).Return(5, nil)

	var interceptorCalled bool
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		interceptorCalled = true
		assert.Equal(t, "File.Read", op)

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	buf := make([]byte, 10)
	n, err := wrapped.Read(buf)

	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", string(buf[:n]))
	assert.True(t, interceptorCalled)
}

// TestWrappedFile_ReadAt verifies that ReadAt calls the inner file correctly.
func TestWrappedFile_ReadAt(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().ReadAt(mock.Anything, int64(10)).Run(func(p []byte, off int64) {
		copy(p, "world")
	}).Return(5, nil)

	var capturedArgs []any
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		capturedArgs = args

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	buf := make([]byte, 10)
	n, err := wrapped.ReadAt(buf, 10)

	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Len(t, capturedArgs, 2)
	assert.Equal(t, int64(10), capturedArgs[1])
}

// TestWrappedFile_Seek verifies that Seek calls the inner file correctly.
func TestWrappedFile_Seek(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Seek(int64(100), 0).Return(int64(100), nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	pos, err := wrapped.Seek(100, 0)

	require.NoError(t, err)
	assert.Equal(t, int64(100), pos)
}

// TestWrappedFile_Close verifies that Close calls the inner file.
func TestWrappedFile_Close(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Close().Return(nil)

	var interceptorCalled bool
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		interceptorCalled = true
		assert.Equal(t, "File.Close", op)

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	err := wrapped.Close()

	require.NoError(t, err)
	assert.True(t, interceptorCalled)
}

// TestWrappedFile_Lock verifies that Lock calls the inner file.
func TestWrappedFile_Lock(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Lock().Return(nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	err := wrapped.Lock()

	require.NoError(t, err)
}

// TestWrappedFile_Unlock verifies that Unlock calls the inner file.
func TestWrappedFile_Unlock(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Unlock().Return(nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	err := wrapped.Unlock()

	require.NoError(t, err)
}

// TestWrappedFile_Truncate verifies that Truncate calls the inner file.
func TestWrappedFile_Truncate(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Truncate(int64(1024)).Return(nil)

	var capturedArgs []any
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		capturedArgs = args

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	err := wrapped.Truncate(1024)

	require.NoError(t, err)
	assert.Equal(t, []any{int64(1024)}, capturedArgs)
}

// TestWrappedFile_Name verifies that Name returns the inner file's name
// without going through the chain.
func TestWrappedFile_Name(t *testing.T) {
	t.Parallel()

	mockFile := nfsproxymocks.NewMockFile(t)
	mockFile.EXPECT().Name().Return("/path/to/file.txt")

	chain := middleware.NewChain()
	wrapped := middleware.WrapFile(context.Background(), mockFile, chain)

	name := wrapped.Name()

	assert.Equal(t, "/path/to/file.txt", name)
}

// TestWrapFilesystem_ReturnsNilForNilInput verifies that WrapFilesystem
// returns nil when given a nil filesystem.
func TestWrapFilesystem_ReturnsNilForNilInput(t *testing.T) {
	t.Parallel()

	chain := middleware.NewChain()
	result := middleware.WrapFilesystem(context.Background(), nil, chain)

	assert.Nil(t, result)
}

// TestWrappedFS_Create verifies that Create calls the inner filesystem
// and wraps the returned file.
func TestWrappedFS_Create(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFile := nfsproxymocks.NewMockFile(t)
	mockFS.EXPECT().Create("/test.txt").Return(mockFile, nil)

	var interceptorCalled bool
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		interceptorCalled = true
		assert.Equal(t, "FS.Create", op)
		assert.Equal(t, []any{"/test.txt"}, args)

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	file, err := wrapped.Create("/test.txt")

	require.NoError(t, err)
	assert.NotNil(t, file)
	assert.True(t, interceptorCalled)
}

// TestWrappedFS_Create_WithError verifies that Create returns both
// the file and error when the inner operation fails.
func TestWrappedFS_Create_WithError(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Create("/test.txt").Return(nil, errTest)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	file, err := wrapped.Create("/test.txt")

	require.ErrorIs(t, err, errTest)
	assert.Nil(t, file)
}

// TestWrappedFS_Open verifies that Open calls the inner filesystem.
func TestWrappedFS_Open(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFile := nfsproxymocks.NewMockFile(t)
	mockFS.EXPECT().Open("/test.txt").Return(mockFile, nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	file, err := wrapped.Open("/test.txt")

	require.NoError(t, err)
	assert.NotNil(t, file)
}

// TestWrappedFS_OpenFile verifies that OpenFile calls the inner filesystem.
func TestWrappedFS_OpenFile(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFile := nfsproxymocks.NewMockFile(t)
	mockFS.EXPECT().OpenFile("/test.txt", os.O_RDWR, os.FileMode(0o644)).Return(mockFile, nil)

	var capturedArgs []any
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		capturedArgs = args

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	file, err := wrapped.OpenFile("/test.txt", os.O_RDWR, 0o644)

	require.NoError(t, err)
	assert.NotNil(t, file)
	assert.Equal(t, "/test.txt", capturedArgs[0])
	assert.Equal(t, os.O_RDWR, capturedArgs[1])
	assert.Equal(t, os.FileMode(0o644), capturedArgs[2])
}

// TestWrappedFS_Stat verifies that Stat calls the inner filesystem.
func TestWrappedFS_Stat(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockInfo := &mockFileInfo{name: "test.txt", size: 1024}
	mockFS.EXPECT().Stat("/test.txt").Return(mockInfo, nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	info, err := wrapped.Stat("/test.txt")

	require.NoError(t, err)
	assert.Equal(t, "test.txt", info.Name())
	assert.Equal(t, int64(1024), info.Size())
}

// TestWrappedFS_Rename verifies that Rename calls the inner filesystem.
func TestWrappedFS_Rename(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Rename("/old.txt", "/new.txt").Return(nil)

	var capturedArgs []any
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		capturedArgs = args

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	err := wrapped.Rename("/old.txt", "/new.txt")

	require.NoError(t, err)
	assert.Equal(t, []any{"/old.txt", "/new.txt"}, capturedArgs)
}

// TestWrappedFS_Remove verifies that Remove calls the inner filesystem.
func TestWrappedFS_Remove(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Remove("/test.txt").Return(nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	err := wrapped.Remove("/test.txt")

	require.NoError(t, err)
}

// TestWrappedFS_Join verifies that Join calls the inner filesystem
// without going through the chain.
func TestWrappedFS_Join(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Join(mock.Anything).Return("/path/to/file.txt")

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	result := wrapped.Join("path", "to", "file.txt")

	assert.Equal(t, "/path/to/file.txt", result)
}

// TestWrappedFS_TempFile verifies that TempFile calls the inner filesystem.
func TestWrappedFS_TempFile(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFile := nfsproxymocks.NewMockFile(t)
	mockFS.EXPECT().TempFile("/tmp", "prefix").Return(mockFile, nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	file, err := wrapped.TempFile("/tmp", "prefix")

	require.NoError(t, err)
	assert.NotNil(t, file)
}

// TestWrappedFS_ReadDir verifies that ReadDir calls the inner filesystem.
func TestWrappedFS_ReadDir(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	infos := []os.FileInfo{
		&mockFileInfo{name: "file1.txt"},
		&mockFileInfo{name: "file2.txt"},
	}
	mockFS.EXPECT().ReadDir("/dir").Return(infos, nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	result, err := wrapped.ReadDir("/dir")

	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "file1.txt", result[0].Name())
	assert.Equal(t, "file2.txt", result[1].Name())
}

// TestWrappedFS_MkdirAll verifies that MkdirAll calls the inner filesystem.
func TestWrappedFS_MkdirAll(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().MkdirAll("/path/to/dir", os.FileMode(0o755)).Return(nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	err := wrapped.MkdirAll("/path/to/dir", 0o755)

	require.NoError(t, err)
}

// TestWrappedFS_Lstat verifies that Lstat calls the inner filesystem.
func TestWrappedFS_Lstat(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockInfo := &mockFileInfo{name: "link.txt"}
	mockFS.EXPECT().Lstat("/link.txt").Return(mockInfo, nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	info, err := wrapped.Lstat("/link.txt")

	require.NoError(t, err)
	assert.Equal(t, "link.txt", info.Name())
}

// TestWrappedFS_Symlink verifies that Symlink calls the inner filesystem.
func TestWrappedFS_Symlink(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Symlink("/target", "/link").Return(nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	err := wrapped.Symlink("/target", "/link")

	require.NoError(t, err)
}

// TestWrappedFS_Readlink verifies that Readlink calls the inner filesystem.
func TestWrappedFS_Readlink(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Readlink("/link").Return("/target", nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	target, err := wrapped.Readlink("/link")

	require.NoError(t, err)
	assert.Equal(t, "/target", target)
}

// TestWrappedFS_Chroot verifies that Chroot calls the inner filesystem
// and wraps the returned filesystem.
func TestWrappedFS_Chroot(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockChrootFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Chroot("/subdir").Return(mockChrootFS, nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	chrootFS, err := wrapped.Chroot("/subdir")

	require.NoError(t, err)
	assert.NotNil(t, chrootFS)
}

// TestWrappedFS_Root verifies that Root returns the inner filesystem's root
// without going through the chain.
func TestWrappedFS_Root(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFS.EXPECT().Root().Return("/root/path")

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	root := wrapped.Root()

	assert.Equal(t, "/root/path", root)
}

// TestWrapChange_ReturnsNilForNilInput verifies that WrapChange
// returns nil when given a nil change.
func TestWrapChange_ReturnsNilForNilInput(t *testing.T) {
	t.Parallel()

	chain := middleware.NewChain()
	result := middleware.WrapChange(context.Background(), nil, chain)

	assert.Nil(t, result)
}

// TestWrappedChange_Chmod verifies that Chmod calls the inner change.
func TestWrappedChange_Chmod(t *testing.T) {
	t.Parallel()

	mockChange := nfsproxymocks.NewMockChange(t)
	mockChange.EXPECT().Chmod("/test.txt", os.FileMode(0o755)).Return(nil)

	var capturedArgs []any
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		capturedArgs = args

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapChange(context.Background(), mockChange, chain)

	err := wrapped.Chmod("/test.txt", 0o755)

	require.NoError(t, err)
	assert.Equal(t, "/test.txt", capturedArgs[0])
	assert.Equal(t, os.FileMode(0o755), capturedArgs[1])
}

// TestWrappedChange_Lchown verifies that Lchown calls the inner change.
func TestWrappedChange_Lchown(t *testing.T) {
	t.Parallel()

	mockChange := nfsproxymocks.NewMockChange(t)
	mockChange.EXPECT().Lchown("/test.txt", 1000, 1000).Return(nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapChange(context.Background(), mockChange, chain)

	err := wrapped.Lchown("/test.txt", 1000, 1000)

	require.NoError(t, err)
}

// TestWrappedChange_Chown verifies that Chown calls the inner change.
func TestWrappedChange_Chown(t *testing.T) {
	t.Parallel()

	mockChange := nfsproxymocks.NewMockChange(t)
	mockChange.EXPECT().Chown("/test.txt", 1000, 1000).Return(nil)

	chain := middleware.NewChain()
	wrapped := middleware.WrapChange(context.Background(), mockChange, chain)

	err := wrapped.Chown("/test.txt", 1000, 1000)

	require.NoError(t, err)
}

// TestWrappedChange_Chtimes verifies that Chtimes calls the inner change.
func TestWrappedChange_Chtimes(t *testing.T) {
	t.Parallel()

	mockChange := nfsproxymocks.NewMockChange(t)
	atime := time.Now()
	mtime := time.Now().Add(-time.Hour)
	mockChange.EXPECT().Chtimes("/test.txt", atime, mtime).Return(nil)

	var capturedArgs []any
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		capturedArgs = args

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapChange(context.Background(), mockChange, chain)

	err := wrapped.Chtimes("/test.txt", atime, mtime)

	require.NoError(t, err)
	assert.Equal(t, "/test.txt", capturedArgs[0])
	assert.Equal(t, atime, capturedArgs[1])
	assert.Equal(t, mtime, capturedArgs[2])
}

// TestWrappedFS_NestedOperations verifies that operations on wrapped files
// returned from wrapped filesystems still go through the interceptor chain.
func TestWrappedFS_NestedOperations(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)
	mockFile := nfsproxymocks.NewMockFile(t)
	mockFS.EXPECT().Create("/test.txt").Return(mockFile, nil)
	mockFile.EXPECT().Write([]byte("hello")).Return(5, nil)
	mockFile.EXPECT().Close().Return(nil)

	var ops []string
	interceptor := func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		ops = append(ops, op)

		return next(ctx)
	}

	chain := middleware.NewChain(interceptor)
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	file, err := wrapped.Create("/test.txt")
	require.NoError(t, err)

	_, err = file.Write([]byte("hello"))
	require.NoError(t, err)

	err = file.Close()
	require.NoError(t, err)

	assert.Equal(t, []string{"FS.Create", "File.Write", "File.Close"}, ops)
}

// TestWrappedFS_Unwrap verifies that the inner filesystem can be unwrapped.
func TestWrappedFS_Unwrap(t *testing.T) {
	t.Parallel()

	mockFS := nfsproxymocks.NewMockFilesystem(t)

	chain := middleware.NewChain()
	wrapped := middleware.WrapFilesystem(context.Background(), mockFS, chain)

	// Type assert to get access to the Unwrap method
	unwrapper, ok := wrapped.(interface{ Unwrap() billy.Filesystem })
	require.True(t, ok)

	inner := unwrapper.Unwrap()
	assert.Equal(t, mockFS, inner)
}

// mockFileInfo implements os.FileInfo for testing.
type mockFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return nil }
