//go:build linux

package build

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// B mid-upload + no Builds[A] entry: Init opens at the hint's (uncompressed)
// path, the chunker's first read hits ErrObjectNotExist, the readSegment
// recovery fetches A's own header sidecar and reopens at the compressed path,
// and the retry succeeds with the decompressed payload.
func TestStorageDiff_RefreshesStaleHintFromAncestorHeader(t *testing.T) {
	t.Parallel()

	const (
		blockSize   = int64(4 << 10)
		frameSizeKB = 256
		payloadSize = 256 * 1024 // single zstd frame
		readLen     = blockSize
	)

	ctx := t.Context()
	aID := uuid.New()
	payload := bytes.Repeat([]byte("ancestor-payload-"), payloadSize/len("ancestor-payload-")+1)[:payloadSize]

	aFrameTable, compressed, _, err := storage.CompressBytes(ctx, payload, storage.CompressConfig{
		Enabled:            true,
		Type:               storage.CompressionZstd.String(),
		Level:              2,
		EncoderConcurrency: 1,
		FrameEncodeWorkers: 1,
		FrameSizeKB:        frameSizeKB,
		MinPartSizeMB:      50,
	})
	require.NoError(t, err)

	aHeader := buildHeader(t, aID, payloadSize, aID)
	aHeader.SetBuild(aID, header.BuildData{Size: int64(payloadSize), FrameData: aFrameTable})
	aHeaderBytes, err := header.SerializeHeader(aHeader)
	require.NoError(t, err)

	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)
	bHeader.IncompletePendingUpload = true

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	// Init opens the hint (uncompressed) path. The chunker's first range
	// fetch surfaces ErrObjectNotExist, triggering the recovery hook.
	uncompressedSeekable := storage.NewMockSeekable(t)
	uncompressedSeekable.EXPECT().Size(mock.Anything).Return(int64(payloadSize), nil)
	uncompressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, storage.ErrObjectNotExist).Once()
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone), mock.Anything).
		Return(uncompressedSeekable, nil).Once()

	// Recovery fetches A's own header from the sidecar.
	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(aHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName), mock.Anything).
		Return(headerBlob, nil).Once()

	// Reopen at the compressed path; the retried read decompresses cleanly.
	compressedSeekable := storage.NewMockSeekable(t)
	compressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(decompressingRangeReader(compressed))
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionZstd), mock.Anything).
		Return(compressedSeekable, nil).Once()

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider, readLen))
}

// Finalized B + nil FrameData on A means "A is uncompressed and that's the
// authoritative answer." The read must serve uncompressed bytes and must not
// fetch A's header sidecar.
func TestStorageDiff_NoRefreshOnFinalizedHeader(t *testing.T) {
	t.Parallel()

	const (
		blockSize   = int64(4 << 10)
		payloadSize = 8 << 10
		readLen     = blockSize
	)

	aID := uuid.New()
	payload := bytes.Repeat([]byte("uncompressed-A-"), payloadSize/len("uncompressed-A-")+1)[:payloadSize]

	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)
	bHeader.SetBuild(aID, header.BuildData{Size: int64(payloadSize), FrameData: nil})

	uncompressedPath := storage.Paths{BuildID: aID.String()}.DataFile(storage.MemfileName, storage.CompressionNone)

	provider := storage.NewMockStorageProvider(t)
	uncompressedSeekable := storage.NewMockSeekable(t)
	uncompressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (io.ReadCloser, error) {
			end := min(off+length, int64(len(payload)))

			return io.NopCloser(bytes.NewReader(payload[off:end])), nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, uncompressedPath, mock.Anything).
		Return(uncompressedSeekable, nil)
	// No expectation on OpenBlob — any header fetch panics the test.

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider, readLen))
}

// runRead wires up a File over a fresh DiffStore + noop metrics and returns
// the first `n` bytes read from offset 0.
func runRead(t *testing.T, h *header.Header, provider storage.StorageProvider, n int64) []byte {
	t.Helper()
	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
	require.NoError(t, err)
	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	f := NewFile(h, store, Memfile, provider, m)

	buf := make([]byte, n)
	got, err := f.ReadAt(t.Context(), buf, 0)
	require.NoError(t, err)
	require.Equal(t, int(n), got)

	return buf
}

// testBlockSize is the block size used by buildHeader. Every header-shape
// test in this file uses the same value; if a future test legitimately
// needs a different one, hoist this back into a parameter.
const testBlockSize = int64(4 << 10)

// buildHeader constructs a V4 header with a single-build self-cover mapping.
func buildHeader(t *testing.T, selfID uuid.UUID, size int64, mapsTo uuid.UUID) *header.Header {
	t.Helper()
	meta := header.NewTemplateMetadata(selfID, uint64(testBlockSize), uint64(size))
	meta.Version = header.MetadataVersionV4
	h, err := header.NewHeader(meta, []header.BuildMap{{
		Offset: 0, Length: uint64(size), BuildId: mapsTo, BuildStorageOffset: 0,
	}})
	require.NoError(t, err)

	return h
}

// decompressingRangeReader returns a RunAndReturn callback that locates the
// requested U-offset in the caller's frame table, slices the compressed
// payload, and streams it through a decompressor. Mirrors what a real
// Seekable does over zstd-compressed data.
func decompressingRangeReader(compressed []byte) func(context.Context, int64, int64, *storage.FrameTable) (io.ReadCloser, error) {
	return func(_ context.Context, offsetU, _ int64, ft *storage.FrameTable) (io.ReadCloser, error) {
		r, err := ft.LocateCompressed(offsetU)
		if err != nil {
			return nil, err
		}
		end := min(r.Offset+int64(r.Length), int64(len(compressed)))

		return storage.NewDecompressingReader(bytes.NewReader(compressed[r.Offset:end]), ft.CompressionType())
	}
}

// fakeRefresher is a stand-in for *StorageDiff in tests that exercise
// specialErrorHandler's decision logic without spinning up the chunker.
// loadedHeader is what RefreshFrameTable returns (nil simulates the
// idempotency-latch short-circuit; non-nil simulates a successful load).
type fakeRefresher struct {
	ft           *storage.FrameTable
	loadedHeader *header.Header
	refreshCalls int
}

func (f *fakeRefresher) FrameTable() *storage.FrameTable { return f.ft }

func (f *fakeRefresher) RefreshFrameTable(context.Context) (*header.Header, error) {
	f.refreshCalls++

	return f.loadedHeader, nil
}

func TestFile_SpecialErrorDecisionMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		err          error
		callerFT     *storage.FrameTable
		latchedFT    *storage.FrameTable
		wantRecovery bool
	}{
		{
			name:         "ObjectNotExist, no caller hint, no latched FT triggers refresh",
			err:          storage.ErrObjectNotExist,
			wantRecovery: true,
		},
		{
			name:         "ObjectNotExist, no caller hint, latched FT still triggers refresh (C1)",
			err:          storage.ErrObjectNotExist,
			latchedFT:    &storage.FrameTable{},
			wantRecovery: true,
		},
		{
			name:         "ObjectNotExist with caller-authoritative hint does not refresh",
			err:          storage.ErrObjectNotExist,
			callerFT:     &storage.FrameTable{},
			wantRecovery: false,
		},
		{
			name:         "PeerTransitionedError triggers refresh after backoff",
			err:          &storage.PeerTransitionedError{},
			wantRecovery: true,
		},
		{
			name:         "Unrelated errors do not trigger refresh",
			err:          errors.New("boom"),
			wantRecovery: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			selfID := uuid.New()
			f := NewFile(buildHeader(t, selfID, 4096, selfID), nil, Memfile, nil, blockmetrics.Metrics{})
			refresher := &fakeRefresher{ft: tc.latchedFT}
			seg := readSegment{ft: tc.callerFT}

			herr := f.specialErrorHandler(t.Context(), refresher, seg, tc.err)
			if tc.wantRecovery {
				require.NoError(t, herr, "expected recovery to succeed and signal retry")
				require.Equal(t, 1, refresher.refreshCalls)
			} else {
				require.ErrorIs(t, herr, tc.err, "expected original error to propagate without refresh")
				require.Zero(t, refresher.refreshCalls)
			}
		})
	}
}

// TestFile_SpecialErrorHandlerPromotesSelfHeader covers the post-refresh
// SwapHeader hook. When the refreshed header's buildID matches the File's
// own, recovery promotes it; an ancestor header (different buildID) is
// dropped, leaving File.header untouched.
func TestFile_SpecialErrorHandlerPromotesSelfHeader(t *testing.T) {
	t.Parallel()

	selfID := uuid.New()
	ancestorID := uuid.New()

	cases := []struct {
		name          string
		loadedBuildID uuid.UUID
		wantSwapped   bool
	}{
		{
			name:          "self uuid match promotes loaded header",
			loadedBuildID: selfID,
			wantSwapped:   true,
		},
		{
			name:          "ancestor uuid leaves File.header untouched",
			loadedBuildID: ancestorID,
			wantSwapped:   false,
		},
		{
			name:          "idempotency-latch nil header is a no-op",
			loadedBuildID: uuid.Nil, // sentinel meaning "fakeRefresher returns nil"
			wantSwapped:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			initial := buildHeader(t, selfID, 4096, selfID)
			f := NewFile(initial, nil, Memfile, nil, blockmetrics.Metrics{})

			var loaded *header.Header
			if tc.loadedBuildID != uuid.Nil {
				loaded = buildHeader(t, tc.loadedBuildID, 4096, tc.loadedBuildID)
			}
			refresher := &fakeRefresher{loadedHeader: loaded}
			seg := readSegment{}

			require.NoError(t, f.specialErrorHandler(t.Context(), refresher, seg, storage.ErrObjectNotExist))
			require.Equal(t, 1, refresher.refreshCalls)

			if tc.wantSwapped {
				require.Same(t, loaded, f.Header(), "expected File.header to be swapped to the loaded header")
			} else {
				require.Same(t, initial, f.Header(), "expected File.header to remain the initial one")
			}
		})
	}
}
