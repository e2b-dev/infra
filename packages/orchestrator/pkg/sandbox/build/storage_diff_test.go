//go:build linux

package build

import (
	"bytes"
	"context"
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

// Parent had no Builds[A] entry (uncompressedSize = 0). Init must load A's
// own header before opening upstream, learn the CT from there, and
// open only the compressed path. The read serves decompressed bytes on the
// first try — no retry loop, no failed open at the uncompressed path.
func TestStorageDiff_LoadsOwnHeaderWhenParentHasNoEntry(t *testing.T) {
	t.Parallel()

	const (
		blockSize   = int64(4 << 10)
		frameSizeKB = 256
		payloadSize = 256 * 1024
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
	aHeader.SetBuild(aID, header.BuildData{Size: int64(payloadSize), FrameData: aFrameTable.Table()})
	aHeaderBytes, err := header.SerializeHeader(aHeader)
	require.NoError(t, err)

	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)
	bHeader.IncompletePendingUpload = true

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	// Peer-routing probe: non-PeerRouted result → fall through to refresh.
	probeSeekable := storage.NewMockSeekable(t)
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(probeSeekable, nil).Once()

	// Then load A's own header.
	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(aHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName)).
		Return(headerBlob, nil).Once()

	// Then open upstream at the compressed path; the read decompresses cleanly.
	compressedSeekable := storage.NewMockSeekable(t)
	compressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(decompressingRangeReader(compressed))
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionZstd)).
		Return(compressedSeekable, nil).Once()

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider))
}

// When the proactive header load returns a header whose BuildId matches the
// File's own current header, getBuild promotes it via SwapHeader. Subsequent
// reads then pick up the loaded header's authoritative Builds map directly.
func TestStorageDiff_SwapsHeaderOnSelfMatch(t *testing.T) {
	t.Parallel()

	const (
		blockSize   = int64(4 << 10)
		frameSizeKB = 256
		payloadSize = 256 * 1024
		readLen     = blockSize
	)

	ctx := t.Context()
	selfID := uuid.New()
	payload := bytes.Repeat([]byte("self-payload-"), payloadSize/len("self-payload-")+1)[:payloadSize]

	frameTable, compressed, _, err := storage.CompressBytes(ctx, payload, storage.CompressConfig{
		Enabled:            true,
		Type:               storage.CompressionZstd.String(),
		Level:              2,
		EncoderConcurrency: 1,
		FrameEncodeWorkers: 1,
		FrameSizeKB:        frameSizeKB,
		MinPartSizeMB:      50,
	})
	require.NoError(t, err)

	// Sidecar header: V4, self-identified, with a populated Builds map.
	fullHeader := buildHeader(t, selfID, payloadSize, selfID)
	fullHeader.SetBuild(selfID, header.BuildData{Size: int64(payloadSize), FrameData: frameTable.Table()})
	fullHeaderBytes, err := header.SerializeHeader(fullHeader)
	require.NoError(t, err)

	// File's current header: same BuildId, but Builds map is empty — forces
	// the proactive header load when reading the self mapping.
	staleHeader := buildHeader(t, selfID, payloadSize, selfID)

	paths := storage.Paths{BuildID: selfID.String()}
	provider := storage.NewMockStorageProvider(t)

	// Peer-routing probe: non-PeerRouted result → fall through to refresh.
	probeSeekable := storage.NewMockSeekable(t)
	provider.EXPECT().
		OpenSeekable(mock.Anything, paths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(probeSeekable, nil).Once()

	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(fullHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, paths.HeaderFile(storage.MemfileName)).
		Return(headerBlob, nil).Once()

	compressedSeekable := storage.NewMockSeekable(t)
	compressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(decompressingRangeReader(compressed))
	provider.EXPECT().
		OpenSeekable(mock.Anything, paths.DataFile(storage.MemfileName, storage.CompressionZstd)).
		Return(compressedSeekable, nil).Once()

	got, f := runReadOnFile(t, staleHeader, provider)
	require.Equal(t, payload[:readLen], got)
	require.NotSame(t, staleHeader, f.Header(), "File.header should have been swapped")
	swapped := f.Header()
	require.Equal(t, selfID, swapped.Metadata.BuildId, "swapped header should retain self BuildId")
	require.Contains(t, swapped.Builds, selfID, "swapped header should carry the loaded Builds entry")
	require.Empty(t, staleHeader.Builds, "stale header's empty Builds map proves the swap was needed")
}

// Finalized B + nil FrameData on A means "A is uncompressed and that's the
// authoritative answer." Init must take the parent's word for it and not
// fetch A's header.
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
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))

			return storage.NewRangeReader(io.NopCloser(bytes.NewReader(payload[off:end]))), storage.SourceFS, nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, uncompressedPath).
		Return(uncompressedSeekable, nil)
	// No expectation on OpenBlob — any header fetch panics the test.

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider))
}

// When a peer is known to be P2P-serving the ancestor and our parent header
// lacks its Builds entry, create must NOT fetch the ancestor's header —
// that refresh would just return the same in-progress header over P2P.
// Instead, open at the uncompressed path (which the peer-routed seekable
// serves natively) and let per-read callerFT / peer-transition refresh
// handle CT/FT learning.
func TestStorageDiff_SkipsHeaderRefreshWhenPeerActive(t *testing.T) {
	t.Parallel()

	const (
		blockSize   = int64(4 << 10)
		payloadSize = 8 << 10
		readLen     = blockSize
	)

	aID := uuid.New()
	payload := bytes.Repeat([]byte("p2p-served-"), payloadSize/len("p2p-served-")+1)[:payloadSize]

	// Parent has no Builds[aID] entry — would normally trigger the proactive
	// refresh path.
	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	// Critical: no EXPECT for OpenBlob. peerActiveBootstrapSize discriminates on
	// the PeerRouted marker — we wrap the mock so it counts as peer-routed,
	// otherwise the bootstrap would refresh and the test would panic.
	uncompressedSeekable := storage.NewMockSeekable(t)
	uncompressedSeekable.EXPECT().
		Size(mock.Anything).
		Return(int64(payloadSize), nil).Once()
	uncompressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))

			return storage.NewRangeReader(io.NopCloser(bytes.NewReader(payload[off:end]))), storage.SourceFS, nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(peerRoutedSeekable{Seekable: uncompressedSeekable}, nil).Once()

	got, _ := runReadOnFile(t, bHeader, provider)
	require.Equal(t, payload[:readLen], got)
}

// V4+ self missing ancestor entry refreshes from storage; when the loaded
// ancestor is V3, openFromLoadedHeader opens at the basic path.
func TestStorageDiff_V3AncestorFallsBackToUncompressed(t *testing.T) {
	t.Parallel()

	const payloadSize = 256 * 1024
	readLen := testBlockSize

	aID := uuid.New()
	payload := bytes.Repeat([]byte("v3-ancestor-data-"), payloadSize/len("v3-ancestor-data-")+1)[:payloadSize]

	// A's header on storage: V3 (no Builds map).
	aMeta := header.NewTemplateMetadata(aID, uint64(testBlockSize), uint64(payloadSize))
	aMeta.Version = 3
	aHeader, err := header.NewHeader(aMeta, []header.BuildMap{{
		Offset: 0, Length: uint64(payloadSize), BuildId: aID, BuildStorageOffset: 0,
	}})
	require.NoError(t, err)
	aHeaderBytes, err := header.SerializeHeader(aHeader)
	require.NoError(t, err)

	// Current header: complete V4, maps to A, no Builds entry for A.
	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	// Probe open at basic path (peer-routing detection). Non-PeerRouted →
	// initialSize falls through to refresh.
	probeSeekable := storage.NewMockSeekable(t)
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(probeSeekable, nil).Once()

	// Refresh A's header — V3, so openFromLoadedHeader opens at the basic path.
	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(aHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName)).
		Return(headerBlob, nil).Once()

	rawSeekable := storage.NewMockSeekable(t)
	rawSeekable.EXPECT().
		Size(mock.Anything).
		Return(int64(payloadSize), nil).Once()
	rawSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))

			return storage.NewRangeReader(io.NopCloser(bytes.NewReader(payload[off:end]))), storage.SourceFS, nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(rawSeekable, nil).Once()

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider))
}

// Same V3 fallback on the read-time refresh path: a StorageDiff with no
// latched FT and no caller FT funnels through reloadSource, which must latch
// a pre-V4 header as authoritatively uncompressed rather than failing the
// self-entry lookup.
func TestStorageDiff_ReloadSourceLatchesV3AsUncompressed(t *testing.T) {
	t.Parallel()

	const payloadSize = 64 * 1024
	readLen := testBlockSize

	aID := uuid.New()
	payload := bytes.Repeat([]byte("v3-reload-data-"), payloadSize/len("v3-reload-data-")+1)[:payloadSize]

	aMeta := header.NewTemplateMetadata(aID, uint64(testBlockSize), uint64(payloadSize))
	aHeader, err := header.NewHeader(aMeta, []header.BuildMap{{
		Offset: 0, Length: uint64(payloadSize), BuildId: aID, BuildStorageOffset: 0,
	}})
	require.NoError(t, err)
	aHeaderBytes, err := header.SerializeHeader(aHeader)
	require.NoError(t, err)

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(aHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName)).
		Return(headerBlob, nil).Once()

	rawSeekable := storage.NewMockSeekable(t)
	rawSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))

			return storage.NewRangeReader(io.NopCloser(bytes.NewReader(payload[off:end]))), storage.SourceFS, nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(rawSeekable, nil).Once()

	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)

	// Bootstrap with no latched FT (initialFT nil) — the first read with a nil
	// caller FT must refresh and latch the V3 header as uncompressed.
	bootstrap := storage.NewMockSeekable(t)
	diff, err := newStorageDiff(t.TempDir(), aID.String(), Memfile, storage.MemfileObjectType,
		testBlockSize, m, provider, bootstrap, int64(payloadSize), nil, "", &featureflags.Client{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = diff.Close() })

	buf := make([]byte, readLen)
	n, err := diff.ReadAt(t.Context(), buf, 0, nil)
	require.NoError(t, err)
	require.Equal(t, int(readLen), n)
	require.Equal(t, payload[:readLen], buf)
}

// Peer-served current header with no Builds entry for a legacy ancestor: the
// ancestor's data file exists at the basic uncompressed path but no header
// file was ever uploaded. createDiff must absorb the ErrObjectNotExist from
// refreshHeader, latch the already-probed basic upstream as uncompressed, and
// serve reads. Pre-fix this returned the wrapped ErrObjectNotExist, bricking
// sandbox resume for any pre-header ancestor referenced from a peer-served
// (Builds-less) header.
func TestStorageDiff_MissingAncestorHeaderFallsBackToUncompressed(t *testing.T) {
	t.Parallel()

	const payloadSize = 256 * 1024
	readLen := testBlockSize

	aID := uuid.New()
	payload := bytes.Repeat([]byte("legacy-ancestor-"), payloadSize/len("legacy-ancestor-")+1)[:payloadSize]

	// Current header: V4, maps to A, no Builds entry for A (simulates a
	// peer-served header where backfillMissingV3UncompressedBuilds was skipped).
	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	// One open at the basic uncompressed path: the probe seekable doubles as
	// the read upstream once the fallback latches it.
	rawSeekable := storage.NewMockSeekable(t)
	rawSeekable.EXPECT().
		Size(mock.Anything).
		Return(int64(payloadSize), nil).Once()
	rawSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))

			return storage.NewRangeReader(io.NopCloser(bytes.NewReader(payload[off:end]))), storage.SourceFS, nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(rawSeekable, nil).Once()

	// Header file is missing — the very-old-template case.
	provider.EXPECT().
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName)).
		Return(nil, storage.ErrObjectNotExist).Once()

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider))
}

// Storage-loaded current header carrying a zero-valued Builds entry for an
// ancestor (LoadHeader's backfillMissingV3UncompressedBuilds marker for a
// V3-or-older ancestor). createDiff must latch UncompressedFullFrameTable at
// construction so the runtime read path never refreshes — that ancestor's
// header file may not exist, and a runtime ErrObjectNotExist would be
// indistinguishable from a real-entry race.
//
// Reads via diff.ReadAt(ctx, buf, 0, nil) — a nil callerFT, mirroring
// peerserver/seekable.go's Slice(..., nil). File.ReadAt would defeat this
// test by populating callerFT from h.GetBuildFrameData (returns the non-nil
// storage.UncompressedFrameTable for a zero bd, short-circuiting resolve
// before reloadProactive). Without the construction-time latch, this test's
// path is: resolve(nil) → cur.fullDiffFrameTable nil ∧ callerFT nil ∧
// !isPeerRouted → reloadProactive → refreshHeader → an unmocked OpenBlob →
// mockery failure.
func TestStorageDiff_BackfillMarkerLatchesUncompressedAtConstruction(t *testing.T) {
	t.Parallel()

	const payloadSize = 256 * 1024
	readLen := testBlockSize

	aID := uuid.New()
	payload := bytes.Repeat([]byte("backfill-marker-"), payloadSize/len("backfill-marker-")+1)[:payloadSize]

	// Current header maps to A and has Builds[A] = zero (the backfill marker).
	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)
	bHeader.SetBuild(aID, header.BuildData{})

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	rawSeekable := storage.NewMockSeekable(t)
	rawSeekable.EXPECT().
		Size(mock.Anything).
		Return(int64(payloadSize), nil).Once()
	rawSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))

			return storage.NewRangeReader(io.NopCloser(bytes.NewReader(payload[off:end]))), storage.SourceFS, nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone)).
		Return(rawSeekable, nil).Once()
	// No OpenBlob expectation: refresh path must NOT fire.

	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
	require.NoError(t, err)
	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	f := NewFile(bHeader, store, Memfile, provider, m)

	diff, err := f.getBuild(t.Context(), aID)
	require.NoError(t, err)

	buf := make([]byte, readLen)
	n, err := diff.ReadAt(t.Context(), buf, 0, nil)
	require.NoError(t, err)
	require.Equal(t, int(readLen), n)
	require.Equal(t, payload[:readLen], buf)
}

// runRead wires up a File over a fresh DiffStore + noop metrics and returns
// the first testBlockSize bytes read from offset 0.
func runRead(t *testing.T, h *header.Header, provider storage.StorageProvider) []byte {
	t.Helper()
	buf, _ := runReadOnFile(t, h, provider)

	return buf
}

// runReadOnFile is runRead's variant that exposes the File so callers can
// inspect post-read state (e.g. SwapHeader fired).
func runReadOnFile(t *testing.T, h *header.Header, provider storage.StorageProvider) ([]byte, *File) {
	t.Helper()
	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
	require.NoError(t, err)
	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	f := NewFile(h, store, Memfile, provider, m)

	buf := make([]byte, testBlockSize)
	got, err := f.ReadAt(t.Context(), buf, 0)
	require.NoError(t, err)
	require.Equal(t, int(testBlockSize), got)

	return buf, f
}

// peerRoutedSeekable wraps a Seekable to satisfy the peerclient.PeerRouted
// marker — production peer routing wraps the base Seekable in peerSeekable,
// which advertises IsPeerRouted() == true. Tests that need to take the
// peer-routed branch in newStorageDiff use this shim.
type peerRoutedSeekable struct {
	storage.Seekable
}

func (peerRoutedSeekable) IsPeerRouted() {}

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
func decompressingRangeReader(compressed []byte) func(context.Context, int64, int64, *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	return func(_ context.Context, offsetU, _ int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
		r, err := ft.LocateCompressed(offsetU)
		if err != nil {
			return nil, storage.SourceFS, err
		}
		end := min(r.Offset+int64(r.Length), int64(len(compressed)))

		rc, err := storage.NewDecompressReader(storage.NewRangeReader(io.NopCloser(bytes.NewReader(compressed[r.Offset:end]))), ft.CompressionType(), storage.UnknownSource, storage.UnknownSeekableObjectType)

		return rc, storage.SourceFS, err
	}
}
