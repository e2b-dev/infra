//go:build linux

package block

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A healthy exclusive writer is a generic Cache compatibility control, not a
// production writer on Chunker's private cache. Real eviction is tested in build.
func TestChunkerReadAtCacheLockWait(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"cancelled", "healthy-cancellable", "healthy-background"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			data := bytes.Repeat([]byte{0x42}, testBlockSize)
			chunker := newTestChunker(t, int64(len(data)))
			t.Cleanup(func() { require.NoError(t, chunker.Close()) })
			upstream := &fakeSeekable{data: data}
			buf := make([]byte, testBlockSize)
			_, err := chunker.ReadAt(t.Context(), buf, 0, upstream, nil)
			require.NoError(t, err)
			require.Equal(t, data, buf)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "healthy-background" {
				ctx = context.WithoutCancel(t.Context())
			}
			chunker.cache.mu.Lock()
			unlocked := false
			out := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !unlocked {
					chunker.cache.mu.Unlock()
				}
				if !joined {
					select {
					case <-out:
					case <-time.After(time.Second):
						t.Error("cache reader cleanup did not finish")
					}
				}
			}()
			go func() {
				_, readErr := chunker.ReadAt(ctx, buf, 0, upstream, nil)
				out <- readErr
			}()

			premature := false
			select {
			case err = <-out:
				premature, joined = true, true
			case <-time.After(20 * time.Millisecond):
			}
			beforeUnlock := premature
			if mode == "cancelled" && !joined {
				cancel()
				select {
				case err = <-out:
					beforeUnlock, joined = true, true
				case <-time.After(200 * time.Millisecond):
				}
			}
			chunker.cache.mu.Unlock()
			unlocked = true
			if !joined {
				select {
				case err = <-out:
					joined = true
				case <-time.After(time.Second):
					t.Fatal("cache reader did not finish after unlocking")
				}
			}

			require.False(t, premature, "lock contention must not become a closed-cache error")
			if mode == "cancelled" {
				require.True(t, beforeUnlock, "caller cancellation waited for the unrelated writer")
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
				require.Equal(t, data, buf)
			}
			require.EqualValues(t, 1, upstream.fetchCount.Load(), "lock wait must not refetch cached bytes")
		})
	}
}
