//go:build ignore
// +build ignore

// TODO: tests carried over from old API (io.ReadCloser, 2-return OpenRangeReader).
// Rewrite to match current RangeReader / 3-return API before un-ignoring.

package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// closeTracker wraps a ReadCloser and signals when Close is called.
type closeTracker struct {
	io.ReadCloser

	closed chan struct{}
}

func newCloseTracker(rc io.ReadCloser) *closeTracker {
	return &closeTracker{ReadCloser: rc, closed: make(chan struct{})}
}

func (ct *closeTracker) Close() error {
	defer close(ct.closed)

	return ct.ReadCloser.Close()
}

// concurrentFlags returns a mock that enables ConcurrentNFSCacheCheckFlag.
func concurrentFlags(t *testing.T) *MockFeatureFlagsClient {
	t.Helper()

	ff := NewMockFeatureFlagsClient(t)
	ff.EXPECT().
		BoolFlag(mock.Anything, featureflags.ConcurrentNFSCacheCheckFlag).
		Return(true)

	return ff
}

func TestOpenRangeReader_UncompressedConcurrent(t *testing.T) {
	t.Parallel()

	t.Run("NFS hit cancels inner and closes its reader", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

		innerCancelled := make(chan struct{})
		tracker := newCloseTracker(io.NopCloser(bytes.NewReader(data)))
		mockSeeker := NewMockSeekable(t)
		mockSeeker.EXPECT().
			OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, (*FrameTable)(nil)).
			RunAndReturn(func(ctx context.Context, _ int64, _ int64, _ *FrameTable) (io.ReadCloser, error) {
				go func() {
					<-ctx.Done()
					close(innerCancelled)
				}()

				return tracker, nil
			})

		c := cachedSeekable{
			path:      tempDir,
			chunkSize: int64(len(data)),
			inner:     mockSeeker,
			flags:     concurrentFlags(t),
			tracer:    noopTracer,
		}

		// Plant a cache file so NFS wins.
		chunkPath := c.makeChunkFilename(0)
		require.NoError(t, os.MkdirAll(filepath.Dir(chunkPath), 0o755))
		require.NoError(t, os.WriteFile(chunkPath, data, 0o600))

		rc, err := c.OpenRangeReader(t.Context(), 0, int64(len(data)), nil)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, data, got)
		require.NoError(t, rc.Close())

		select {
		case <-innerCancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("inner context was not cancelled after NFS hit")
		}

		select {
		case <-tracker.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("inner reader was not closed after NFS hit")
		}
	})

	t.Run("NFS miss uses inner with head start", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		data := []byte("hello")

		mockSeeker := NewMockSeekable(t)
		mockSeeker.EXPECT().
			OpenRangeReader(mock.Anything, int64(0), int64(len(data)), (*FrameTable)(nil)).
			Return(io.NopCloser(bytes.NewReader(data)), nil).
			Once()

		c := cachedSeekable{
			path:      tempDir,
			chunkSize: 10,
			inner:     mockSeeker,
			flags:     concurrentFlags(t),
			tracer:    noopTracer,
		}

		rc, err := c.OpenRangeReader(t.Context(), 0, int64(len(data)), nil)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, data, got)
		require.NoError(t, rc.Close())

		c.wg.Wait()

		// Cache should be populated for next call (mock would fail if called again).
		c.flags = nil
		rc2, err := c.OpenRangeReader(t.Context(), 0, int64(len(data)), nil)
		require.NoError(t, err)

		got2, err := io.ReadAll(rc2)
		require.NoError(t, err)
		assert.Equal(t, data, got2)
		require.NoError(t, rc2.Close())
	})

	t.Run("skip writeback does not populate cache", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		data := []byte("hello")

		mockSeeker := NewMockSeekable(t)
		mockSeeker.EXPECT().
			OpenRangeReader(mock.Anything, int64(0), int64(len(data)), (*FrameTable)(nil)).
			Return(io.NopCloser(bytes.NewReader(data)), nil)

		c := cachedSeekable{
			path:      tempDir,
			chunkSize: 10,
			inner:     mockSeeker,
			flags:     concurrentFlags(t),
			tracer:    noopTracer,
		}

		ctx := WithSkipCacheWriteback(t.Context())
		rc, err := c.OpenRangeReader(ctx, 0, int64(len(data)), nil)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, data, got)
		require.NoError(t, rc.Close())

		c.wg.Wait()

		chunkPath := c.makeChunkFilename(0)
		_, err = os.Stat(chunkPath)
		assert.True(t, os.IsNotExist(err), "cache writeback should be skipped")
	})

	t.Run("cancel is called on reader Close", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		data := []byte("hello")

		cancelCalled := make(chan struct{})
		mockSeeker := NewMockSeekable(t)
		mockSeeker.EXPECT().
			OpenRangeReader(mock.Anything, int64(0), int64(len(data)), (*FrameTable)(nil)).
			RunAndReturn(func(ctx context.Context, _ int64, _ int64, _ *FrameTable) (io.ReadCloser, error) {
				go func() {
					<-ctx.Done()
					close(cancelCalled)
				}()

				return io.NopCloser(bytes.NewReader(data)), nil
			})

		c := cachedSeekable{
			path:      tempDir,
			chunkSize: 10,
			inner:     mockSeeker,
			flags:     concurrentFlags(t),
			tracer:    noopTracer,
		}

		ctx := WithSkipCacheWriteback(t.Context())
		rc, err := c.OpenRangeReader(ctx, 0, int64(len(data)), nil)
		require.NoError(t, err)

		_, err = io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())

		select {
		case <-cancelCalled:
		case <-time.After(5 * time.Second):
			t.Fatal("cancel was not called after reader Close")
		}
	})
}

func TestOpenRangeReader_CompressedConcurrent(t *testing.T) {
	t.Parallel()

	original := []byte("the quick brown fox jumps over the lazy dog")
	compressed := lz4Compress(t, original)
	ft := NewFrameTable(CompressionLZ4, []FrameSize{{U: int32(len(original)), C: int32(len(compressed))}})

	t.Run("NFS hit cancels inner and decompresses cached frame", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()

		innerCancelled := make(chan struct{})
		tracker := newCloseTracker(io.NopCloser(bytes.NewReader(compressed)))
		mockSeeker := NewMockSeekable(t)
		mockSeeker.EXPECT().
			OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, (*FrameTable)(nil)).
			RunAndReturn(func(ctx context.Context, _ int64, _ int64, _ *FrameTable) (io.ReadCloser, error) {
				go func() {
					<-ctx.Done()
					close(innerCancelled)
				}()

				return tracker, nil
			})

		c := cachedSeekable{
			path:      tempDir,
			chunkSize: int64(len(original)),
			inner:     mockSeeker,
			flags:     concurrentFlags(t),
			tracer:    noopTracer,
		}

		// Plant compressed frame in NFS cache.
		r, err := ft.LocateCompressed(0)
		require.NoError(t, err)
		framePath := makeFrameFilename(tempDir, r)
		require.NoError(t, os.MkdirAll(filepath.Dir(framePath), 0o755))
		require.NoError(t, os.WriteFile(framePath, compressed, 0o600))

		rc, err := c.OpenRangeReader(t.Context(), 0, int64(len(original)), ft)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, original, got)
		require.NoError(t, rc.Close())

		select {
		case <-innerCancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("inner context was not cancelled after NFS hit")
		}

		select {
		case <-tracker.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("inner reader was not closed after NFS hit")
		}
	})

	t.Run("NFS miss uses inner and caches compressed frame", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()

		mockSeeker := NewMockSeekable(t)
		mockSeeker.EXPECT().
			OpenRangeReader(mock.Anything, int64(0), int64(len(compressed)), (*FrameTable)(nil)).
			Return(io.NopCloser(bytes.NewReader(compressed)), nil).
			Once()

		c := cachedSeekable{
			path:      tempDir,
			chunkSize: int64(len(original)),
			inner:     mockSeeker,
			flags:     concurrentFlags(t),
			tracer:    noopTracer,
		}

		rc, err := c.OpenRangeReader(t.Context(), 0, int64(len(original)), ft)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, original, got)
		require.NoError(t, rc.Close())

		c.wg.Wait()

		// Verify the compressed frame was cached.
		r, err := ft.LocateCompressed(0)
		require.NoError(t, err)
		framePath := makeFrameFilename(tempDir, r)
		cached, err := os.ReadFile(framePath)
		require.NoError(t, err)
		assert.Equal(t, compressed, cached)
	})
}
