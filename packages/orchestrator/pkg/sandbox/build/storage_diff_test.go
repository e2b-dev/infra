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

	// Init loads A's own header — no OpenSeekable at the uncompressed path.
	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(aHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName), mock.Anything).
		Return(headerBlob, nil).Once()

	// Then open upstream at the compressed path; the read decompresses cleanly.
	compressedSeekable := storage.NewMockSeekable(t)
	compressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(decompressingRangeReader(compressed))
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionZstd), mock.Anything).
		Return(compressedSeekable, nil).Once()

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider, readLen))
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

	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(fullHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, paths.HeaderFile(storage.MemfileName), mock.Anything).
		Return(headerBlob, nil).Once()

	compressedSeekable := storage.NewMockSeekable(t)
	compressedSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(decompressingRangeReader(compressed))
	provider.EXPECT().
		OpenSeekable(mock.Anything, paths.DataFile(storage.MemfileName, storage.CompressionZstd), mock.Anything).
		Return(compressedSeekable, nil).Once()

	got, f := runReadOnFile(t, staleHeader, provider, readLen)
	require.Equal(t, payload[:readLen], got)
	require.NotSame(t, staleHeader, f.Header(), "File.header should have been swapped")
	swapped := f.Header()
	require.Equal(t, selfID, swapped.Metadata.BuildId, "swapped header should retain self BuildId")
	require.Contains(t, swapped.Builds, selfID, "swapped header should carry the loaded Builds entry")
	require.Empty(t, staleHeader.Builds, "stale header's empty Builds map proves the swap was needed")
}

// A loaded header that matches self by BuildId but doesn't carry an
// authoritative self entry (mid-P2P upload, V3 with no Builds map, or other
// incomplete state) must NOT be promoted via SwapHeader, AND createDiff must
// fail loudly rather than silently latching the zero-value bd as
// authoritatively uncompressed.
func TestStorageDiff_RejectsLoadedHeaderLackingSelfEntry(t *testing.T) {
	t.Parallel()

	const (
		blockSize   = int64(4 << 10)
		payloadSize = 8 << 10
		readLen     = blockSize
	)

	selfID := uuid.New()

	// Loaded header: self BuildId matches, but Builds map is empty —
	// e.g. mid-P2P state where the peer hasn't populated its self entry.
	incompleteHeader := buildHeader(t, selfID, payloadSize, selfID)
	incompleteBytes, err := header.SerializeHeader(incompleteHeader)
	require.NoError(t, err)

	staleHeader := buildHeader(t, selfID, payloadSize, selfID)
	paths := storage.Paths{BuildID: selfID.String()}
	provider := storage.NewMockStorageProvider(t)

	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(incompleteBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, paths.HeaderFile(storage.MemfileName), mock.Anything).
		Return(headerBlob, nil).Once()

	// No OpenSeekable / Size / OpenRangeReader expectations: refresh must error
	// out before any upstream is opened. If any of these fire the mock fails.

	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute, nil)
	require.NoError(t, err)
	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	f := NewFile(staleHeader, store, Memfile, provider, m)

	_, err = f.ReadAt(t.Context(), make([]byte, readLen), 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no self entry")
	require.Same(t, staleHeader, f.Header(), "File.header must not be swapped to an incomplete loaded one")
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
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (io.ReadCloser, error) {
			end := min(off+length, int64(len(payload)))

			return io.NopCloser(bytes.NewReader(payload[off:end])), nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone), mock.Anything).
		Return(peerRoutedSeekable{Seekable: uncompressedSeekable}, nil).Once()

	got, _ := runReadWithPeers(t, bHeader, provider, readLen, alwaysActivePeer)
	require.Equal(t, payload[:readLen], got)
}

// A V4 snapshot header can map pages to a V3-era ancestor it carries no
// Builds entry for (appendAncestorBuilds skips V3 ancestors). The proactive
// refresh then loads the ancestor's own V3 header — which has no Builds map
// at all — and must latch it as authoritatively uncompressed instead of
// failing the self-entry lookup, which made every resume of a pre-V4
// template EIO on its first uncached read.
func TestStorageDiff_V3AncestorFallsBackToUncompressed(t *testing.T) {
	t.Parallel()

	const payloadSize = 256 * 1024
	readLen := testBlockSize

	aID := uuid.New()
	payload := bytes.Repeat([]byte("v3-ancestor-data-"), payloadSize/len("v3-ancestor-data-")+1)[:payloadSize]

	// A's storage header is V3 (NewTemplateMetadata default): no Builds map.
	aMeta := header.NewTemplateMetadata(aID, uint64(testBlockSize), uint64(payloadSize))
	aHeader, err := header.NewHeader(aMeta, []header.BuildMap{{
		Offset: 0, Length: uint64(payloadSize), BuildId: aID, BuildStorageOffset: 0,
	}})
	require.NoError(t, err)
	aHeaderBytes, err := header.SerializeHeader(aHeader)
	require.NoError(t, err)

	// Current header: V4, maps to A, no Builds entry for A.
	bHeader := buildHeader(t, uuid.New(), payloadSize, aID)

	aPaths := storage.Paths{BuildID: aID.String()}
	provider := storage.NewMockStorageProvider(t)

	headerBlob := storage.NewMockBlob(t)
	headerBlob.EXPECT().
		WriteTo(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
			return io.Copy(w, bytes.NewReader(aHeaderBytes))
		}).Once()
	provider.EXPECT().
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName), mock.Anything).
		Return(headerBlob, nil).Once()

	// The fallback must open the *uncompressed* data path and serve raw bytes.
	rawSeekable := storage.NewMockSeekable(t)
	rawSeekable.EXPECT().
		Size(mock.Anything).
		Return(int64(payloadSize), nil).Once()
	rawSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (io.ReadCloser, error) {
			end := min(off+length, int64(len(payload)))

			return io.NopCloser(bytes.NewReader(payload[off:end])), nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone), mock.Anything).
		Return(rawSeekable, nil).Once()

	require.Equal(t, payload[:readLen], runRead(t, bHeader, provider, readLen))
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
		OpenBlob(mock.Anything, aPaths.HeaderFile(storage.MemfileName), mock.Anything).
		Return(headerBlob, nil).Once()

	rawSeekable := storage.NewMockSeekable(t)
	rawSeekable.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (io.ReadCloser, error) {
			end := min(off+length, int64(len(payload)))

			return io.NopCloser(bytes.NewReader(payload[off:end])), nil
		})
	provider.EXPECT().
		OpenSeekable(mock.Anything, aPaths.DataFile(storage.MemfileName, storage.CompressionNone), mock.Anything).
		Return(rawSeekable, nil).Once()

	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)

	// Bootstrap with no latched FT (initialFT nil) — the first read with a nil
	// caller FT must refresh and latch the V3 header as uncompressed.
	bootstrap := storage.NewMockSeekable(t)
	diff, err := newStorageDiff(t.TempDir(), aID.String(), Memfile, storage.MemfileObjectType,
		testBlockSize, m, provider, nil, bootstrap, int64(payloadSize), nil, &featureflags.Client{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = diff.Close() })

	buf := make([]byte, readLen)
	n, err := diff.ReadAt(t.Context(), buf, 0, nil)
	require.NoError(t, err)
	require.Equal(t, int(readLen), n)
	require.Equal(t, payload[:readLen], buf)
}

// runRead wires up a File over a fresh DiffStore + noop metrics and returns
// the first `n` bytes read from offset 0.
func runRead(t *testing.T, h *header.Header, provider storage.StorageProvider, n int64) []byte {
	t.Helper()
	buf, _ := runReadOnFile(t, h, provider, n)

	return buf
}

// runReadOnFile is runRead's variant that exposes the File so callers can
// inspect post-read state (e.g. SwapHeader fired).
func runReadOnFile(t *testing.T, h *header.Header, provider storage.StorageProvider, n int64) ([]byte, *File) {
	t.Helper()

	return runReadWithPeers(t, h, provider, n, nil)
}

// runReadWithPeers wires up a File with the given IsActivePeer hook so tests
// can drive the IsActive-true branch.
func runReadWithPeers(t *testing.T, h *header.Header, provider storage.StorageProvider, n int64, isActivePeer IsActivePeer) ([]byte, *File) {
	t.Helper()
	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute, isActivePeer)
	require.NoError(t, err)
	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	f := NewFile(h, store, Memfile, provider, m)

	buf := make([]byte, n)
	got, err := f.ReadAt(t.Context(), buf, 0)
	require.NoError(t, err)
	require.Equal(t, int(n), got)

	return buf, f
}

// alwaysActivePeer is an IsActivePeer that reports every build as P2P-active.
func alwaysActivePeer(string) bool { return true }

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
