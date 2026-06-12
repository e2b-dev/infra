package main

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const miB = 1 << 20

// testMapping builds a compact header.Mapping for tests. chunkSize must divide
// every Offset/Length/BuildStorageOffset in maps.
func testMapping(t *testing.T, chunkSize uint64, maps []header.BuildMap) header.Mapping {
	t.Helper()
	m, err := header.NewMapping(chunkSize, maps)
	require.NoError(t, err)

	return m
}

func TestRoleOf(t *testing.T) {
	t.Parallel()

	cur, par, anc := uuid.New(), uuid.New(), uuid.New()
	meta := &header.Metadata{BuildId: cur, BaseBuildId: par}

	require.Equal(t, roleCurrent, roleOf(cur, meta))
	require.Equal(t, roleParent, roleOf(par, meta))
	require.Equal(t, roleAncestor, roleOf(anc, meta))
	require.Equal(t, roleZero, roleOf(uuid.Nil, meta))
}

func TestRatio(t *testing.T) {
	t.Parallel()

	require.Zero(t, ratio(100, 0)) // divide-by-zero guard
	require.InDelta(t, 4.0, ratio(400, 100), 1e-9)
}

func TestChecksumString(t *testing.T) {
	t.Parallel()

	require.Empty(t, checksumString([32]byte{}))

	var cs [32]byte
	cs[0] = 0xAB
	require.True(t, strings.HasPrefix(checksumString(cs), "sha256:ab"))
}

func TestFilteredMappings(t *testing.T) {
	t.Parallel()

	list := []mapping{
		{Offset: 0, Length: 0x100},
		{Offset: 0x100, Length: 0x100},
		{Offset: 0x200, Length: 0x100},
	}

	require.Len(t, filteredMappings(list, span{}), 3) // no filter → all

	got := filteredMappings(list, span{set: true, start: 0x80, end: 0x180})
	require.Len(t, got, 2)
	require.Equal(t, uint64(0), got[0].Offset)
	require.Equal(t, uint64(0x100), got[1].Offset)
}

func TestFilteredFrames(t *testing.T) {
	t.Parallel()

	list := []frameInfo{
		{StartU: 0, EndU: 0x100},
		{StartU: 0x100, EndU: 0x200},
	}

	require.Len(t, filteredFrames(list, span{}), 2)

	got := filteredFrames(list, span{set: true, start: 0x150, end: 0x250})
	require.Len(t, got, 1)
	require.Equal(t, int64(0x100), got[0].StartU)
}

func TestGatherMappings(t *testing.T) {
	t.Parallel()

	cur, anc := uuid.New(), uuid.New()
	h := &header.Header{
		Metadata: &header.Metadata{BuildId: cur, BaseBuildId: cur},
		Mapping: testMapping(t, 50, []header.BuildMap{
			{Offset: 0, Length: 100, BuildId: anc},
			{Offset: 100, Length: 100, BuildId: cur},
			{Offset: 200, Length: 50, BuildId: anc},
			{Offset: 250, Length: 50, BuildId: uuid.Nil},
		}),
	}

	sec, ancestors := gatherMappings(h)

	require.Equal(t, 4, sec.Count)
	require.Equal(t, 1, ancestors) // distinct non-current non-zero builds
	require.Len(t, sec.List, 4)

	// ByBuild is one entry per build, sorted current → ancestor → zero.
	require.Len(t, sec.ByBuild, 3)
	require.Equal(t, roleCurrent, sec.ByBuild[0].Role)
	require.Equal(t, cur, sec.ByBuild[0].BuildID)
	require.Equal(t, uint64(100), sec.ByBuild[0].Bytes)
	require.Equal(t, 1, sec.ByBuild[0].Mappings)

	require.Equal(t, roleAncestor, sec.ByBuild[1].Role)
	require.Equal(t, uint64(150), sec.ByBuild[1].Bytes) // 100 + 50
	require.Equal(t, 2, sec.ByBuild[1].Mappings)

	require.Equal(t, roleZero, sec.ByBuild[2].Role)
}

func TestAncestorIDs(t *testing.T) {
	t.Parallel()

	self, par, anc := uuid.New(), uuid.New(), uuid.New()
	r := &report{
		Image: imageInfo{BuildID: self, BaseBuildID: par},
		Mappings: mappingsSection{ByBuild: []buildExtent{
			{BuildID: self, Role: roleCurrent},
			{BuildID: par, Role: roleParent},
			{BuildID: anc, Role: roleAncestor},
			{BuildID: uuid.Nil, Role: roleZero},
		}},
	}
	require.Equal(t, []uuid.UUID{par, anc}, ancestorIDs(r)) // self and zero excluded

	// A BaseBuildID that no mapping references is still included.
	r2 := &report{
		Image:    imageInfo{BuildID: self, BaseBuildID: par},
		Mappings: mappingsSection{ByBuild: []buildExtent{{BuildID: self, Role: roleCurrent}}},
	}
	require.Equal(t, []uuid.UUID{par}, ancestorIDs(r2))
}

func TestBuildInfoFor(t *testing.T) {
	t.Parallel()

	a, b := uuid.New(), uuid.New()
	r := &report{Builds: []buildInfo{{BuildID: a, Role: roleCurrent}}}

	got, ok := r.buildInfoFor(a)
	require.True(t, ok)
	require.Equal(t, roleCurrent, got.Role)

	_, ok = r.buildInfoFor(b)
	require.False(t, ok)
}

func TestJSONValue(t *testing.T) {
	t.Parallel()

	chain := []*report{{Artifact: "rootfs"}, {Artifact: "memfile"}}

	single, ok := jsonValue(chain, view{}, false).(*report)
	require.True(t, ok)
	require.Equal(t, "rootfs", single.Artifact)

	all, ok := jsonValue(chain, view{}, true).([]*report)
	require.True(t, ok)
	require.Len(t, all, 2)
}

func TestAncestorUsageOf(t *testing.T) {
	t.Parallel()

	anc := uuid.New()
	ancestor := &report{
		Image: imageInfo{BuildID: anc},
		Data: dataSection{
			UncompressedSize: 4 * miB,
			FrameCount:       2,
			Frames: []frameInfo{
				{StartU: 0, EndU: 2 * miB},
				{StartU: 2 * miB, EndU: 4 * miB},
			},
		},
	}
	head := &report{Mappings: mappingsSection{List: []mapping{
		{Offset: 0, Length: 1 * miB, BuildID: anc, StorageOffset: 0},
		{Offset: 1 * miB, Length: 1 * miB, BuildID: uuid.New()}, // a different build
		{Offset: 2 * miB, Length: 0x1000, BuildID: anc, StorageOffset: 3 * miB},
	}}}

	u := ancestorUsageOf(head, ancestor)
	require.Equal(t, 2, u.Mappings)
	require.Equal(t, int64(1*miB+0x1000), u.UsedBytes) // union of the two refs into anc
	require.Equal(t, int64(4*miB), u.DiffBytes)
	require.InDelta(t, float64(1*miB+0x1000)/float64(4*miB), u.UsedFraction, 1e-9)
	require.Equal(t, 2, u.TotalFrames)
	require.Equal(t, 2, u.FramesTouched) // ref [0,1MiB) hits frame 0; [3MiB,..) hits frame 1
}

// TestGatherFetchmap covers the per-chunk cold-restore fetch counter. The
// counter must mirror block.Chunker.locateChunk: a fetch is one frame for
// compressed builds, one MemoryChunkSize-aligned chunk for uncompressed ones,
// and distinct mappings landing in the SAME (build, frame) share a fetch.
//
// Chunk size is 2 MiB throughout (the hugepage / frame size). Mappings inside
// a chunk are at 4 KiB granularity to mirror page-dedup density.
func TestGatherFetchmap(t *testing.T) {
	t.Parallel()

	const (
		bs   = 2 * miB
		page = 4096
	)
	buildA, buildB := uuid.New(), uuid.New()

	// frames(perFrameU): builds a FrameTable with consecutive frames of the
	// given U-sizes, dummy 1 KiB compressed size each.
	frames := func(perFrameU ...int32) *storage.FrameTable {
		sizes := make([]storage.FrameSize, len(perFrameU))
		for i, u := range perFrameU {
			sizes[i] = storage.FrameSize{U: u, C: 1024}
		}

		return storage.NewFullFrameTable(storage.CompressionZstd, sizes).Table()
	}

	// fixture: header with the given mappings, and a Builds map that backs
	// each referenced build with the frame table from builds[id].
	fixture := func(t *testing.T, size uint64, maps []header.BuildMap, builds map[uuid.UUID]*storage.FrameTable) *header.Header {
		t.Helper()
		bm := make(map[uuid.UUID]header.BuildData, len(builds))
		for id, ft := range builds {
			bm[id] = header.BuildData{FrameData: ft}
		}

		return &header.Header{
			Metadata: &header.Metadata{Version: header.MetadataVersionV4, BlockSize: bs, Size: size, BuildId: buildA},
			Mapping:  testMapping(t, page, maps),
			Builds:   bm,
		}
	}

	t.Run("nil for empty header", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, gatherFetchmap(&header.Header{Metadata: &header.Metadata{}}, 0))
		require.Nil(t, gatherFetchmap(&header.Header{Metadata: &header.Metadata{Size: 0}}, bs))
	})

	t.Run("single mapping into one frame: 1 fetch", func(t *testing.T) {
		t.Parallel()
		// Self has one 2 MiB frame at U[0,2MiB); mapping V[0,2MiB) → U[0,2MiB).
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0, Length: bs, BuildId: buildA, BuildStorageOffset: 0},
		}, map[uuid.UUID]*storage.FrameTable{buildA: frames(bs)})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, 1, fm.ChunkCount)
		require.Equal(t, []int{1}, fm.Cells)
		require.Equal(t, 1, fm.MaxLayers)
	})

	t.Run("many small mappings, all in same frame: still 1 fetch", func(t *testing.T) {
		t.Parallel()
		// 4 disjoint 4 KiB pages whose U-offsets are jumpy but all in frame 0.
		// In the OLD storage-run algorithm this was 4 segments — the bug.
		// Production fetches frame 0 once and serves all 4 pages from it.
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0 * page, Length: page, BuildId: buildA, BuildStorageOffset: 0 * page},
			{Offset: 1 * page, Length: page, BuildId: buildA, BuildStorageOffset: 100 * page},
			{Offset: 2 * page, Length: page, BuildId: buildA, BuildStorageOffset: 200 * page},
			{Offset: 3 * page, Length: page, BuildId: buildA, BuildStorageOffset: 300 * page},
			{Offset: 4 * page, Length: bs - 4*page, BuildId: uuid.Nil},
		}, map[uuid.UUID]*storage.FrameTable{buildA: frames(bs)})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{1}, fm.Cells, "all four mappings land in frame 0 → 1 fetch")
	})

	t.Run("mappings into distinct frames of same build: one fetch per frame", func(t *testing.T) {
		t.Parallel()
		// Self has 4 × 0.5 MiB frames covering U[0, 2 MiB); two mappings hit
		// frames 0 and 2 — two distinct fetches.
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0, Length: page, BuildId: buildA, BuildStorageOffset: 0},
			{Offset: page, Length: page, BuildId: buildA, BuildStorageOffset: bs / 2},
			{Offset: 2 * page, Length: bs - 2*page, BuildId: uuid.Nil},
		}, map[uuid.UUID]*storage.FrameTable{buildA: frames(bs/4, bs/4, bs/4, bs/4)})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{2}, fm.Cells)
	})

	t.Run("mappings to distinct builds, same frame index: still distinct fetches", func(t *testing.T) {
		t.Parallel()
		// Each build has its own frame 0 covering [0, 2 MiB). Both fetches
		// are distinct since they target different storage objects.
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0, Length: page, BuildId: buildA, BuildStorageOffset: 0},
			{Offset: page, Length: page, BuildId: buildB, BuildStorageOffset: 0},
			{Offset: 2 * page, Length: bs - 2*page, BuildId: uuid.Nil},
		}, map[uuid.UUID]*storage.FrameTable{
			buildA: frames(bs),
			buildB: frames(bs),
		})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{2}, fm.Cells)
		require.Equal(t, 2, fm.MaxLayers)
	})

	t.Run("alternating A,B,A,B sharing one frame each: 2 fetches", func(t *testing.T) {
		t.Parallel()
		// A's frame 0 covers all of A's data; B's frame 0 covers all of B's
		// data. The orchestrator fetches each frame once for the chunk
		// regardless of how interleaved the mappings are.
		// In the OLD algorithm this was 4 segments — the bug.
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0 * page, Length: page, BuildId: buildA, BuildStorageOffset: 0 * page},
			{Offset: 1 * page, Length: page, BuildId: buildB, BuildStorageOffset: 0 * page},
			{Offset: 2 * page, Length: page, BuildId: buildA, BuildStorageOffset: 1 * page},
			{Offset: 3 * page, Length: page, BuildId: buildB, BuildStorageOffset: 1 * page},
			{Offset: 4 * page, Length: bs - 4*page, BuildId: uuid.Nil},
		}, map[uuid.UUID]*storage.FrameTable{
			buildA: frames(bs),
			buildB: frames(bs),
		})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{2}, fm.Cells, "A's frame 0 + B's frame 0 = 2 fetches")
		require.Equal(t, 2, fm.MaxLayers)
	})

	t.Run("Nil mappings never contribute fetches", func(t *testing.T) {
		t.Parallel()
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0, Length: page, BuildId: buildA, BuildStorageOffset: 0},
			{Offset: page, Length: page, BuildId: uuid.Nil},
			{Offset: 2 * page, Length: page, BuildId: buildA, BuildStorageOffset: 100 * page},
			{Offset: 3 * page, Length: bs - 3*page, BuildId: uuid.Nil},
		}, map[uuid.UUID]*storage.FrameTable{buildA: frames(bs)})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{1}, fm.Cells, "both A mappings hit frame 0 → 1 fetch")
	})

	t.Run("mapping spanning two chunks AND two frames: 1 fetch per chunk", func(t *testing.T) {
		t.Parallel()
		// Two frames of 2 MiB each; a single 4 MiB mapping covers both. Chunk
		// 0 needs frame 0, chunk 1 needs frame 1 — independent fetches.
		h := fixture(t, 2*bs, []header.BuildMap{
			{Offset: 0, Length: 2 * bs, BuildId: buildA, BuildStorageOffset: 0},
		}, map[uuid.UUID]*storage.FrameTable{buildA: frames(bs, bs)})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{1, 1}, fm.Cells)
	})

	t.Run("untouched chunks excluded from averages", func(t *testing.T) {
		t.Parallel()
		h := fixture(t, 4*bs, []header.BuildMap{
			{Offset: 0, Length: page, BuildId: buildA, BuildStorageOffset: 0},
			{Offset: page, Length: 3*bs - page, BuildId: uuid.Nil},
			{Offset: 3 * bs, Length: page, BuildId: buildA, BuildStorageOffset: page},
			{Offset: 3*bs + page, Length: bs - page, BuildId: uuid.Nil},
		}, map[uuid.UUID]*storage.FrameTable{buildA: frames(bs, bs, bs, bs)})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, 4, fm.ChunkCount)
		require.Equal(t, 2, fm.TouchedChunks)
		require.Equal(t, []int{1, 0, 0, 1}, fm.Cells)
		require.InDelta(t, 1.0, fm.AvgSegments, 1e-9, "averaged over touched chunks only")
	})

	t.Run("MaxChunkOff reports the virtual offset of the worst chunk", func(t *testing.T) {
		t.Parallel()
		// Chunk 0: 1 fetch (one mapping into frame 0).
		// Chunk 1: 3 fetches (mappings spanning frames 2, 4, 6 of self).
		// Chunk 2: 2 fetches (mappings spanning frames 8 and 10).
		// frames(bs/4 × 12) gives 12 frames of 0.5 MiB each across U[0, 6 MiB).
		fr := make([]int32, 12)
		for i := range fr {
			fr[i] = bs / 4
		}
		// Each frame is bs/4 wide; frame i starts at U = i*(bs/4).
		const fbs = bs / 4
		h := fixture(t, 3*bs, []header.BuildMap{
			{Offset: 0, Length: page, BuildId: buildA, BuildStorageOffset: 0},
			{Offset: page, Length: bs - page, BuildId: uuid.Nil},
			{Offset: bs + 0*page, Length: page, BuildId: buildA, BuildStorageOffset: 2 * fbs}, // frame 2
			{Offset: bs + 1*page, Length: page, BuildId: buildA, BuildStorageOffset: 4 * fbs}, // frame 4
			{Offset: bs + 2*page, Length: page, BuildId: buildA, BuildStorageOffset: 6 * fbs}, // frame 6
			{Offset: bs + 3*page, Length: bs - 3*page, BuildId: uuid.Nil},
			{Offset: 2*bs + 0*page, Length: page, BuildId: buildA, BuildStorageOffset: 8 * fbs},  // frame 8
			{Offset: 2*bs + 1*page, Length: page, BuildId: buildA, BuildStorageOffset: 10 * fbs}, // frame 10
			{Offset: 2*bs + 2*page, Length: bs - 2*page, BuildId: uuid.Nil},
		}, map[uuid.UUID]*storage.FrameTable{buildA: frames(fr...)})
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{1, 3, 2}, fm.Cells)
		require.Equal(t, 3, fm.MaxSegments)
		require.Equal(t, int64(bs), fm.MaxChunkOff)
	})

	t.Run("uncompressed build uses MemoryChunkSize-aligned chunks", func(t *testing.T) {
		t.Parallel()
		// Two mappings, both into an uncompressed build, separated by less
		// than MemoryChunkSize (4 MiB) in U-space → same prod-fetch chunk.
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0, Length: page, BuildId: buildA, BuildStorageOffset: 0},
			{Offset: page, Length: page, BuildId: buildA, BuildStorageOffset: storage.MemoryChunkSize / 2},
			{Offset: 2 * page, Length: bs - 2*page, BuildId: uuid.Nil},
		}, nil) // no Builds → no frame table → uncompressed path
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{1}, fm.Cells, "both pages in same 4 MiB MemoryChunkSize chunk")
	})

	t.Run("uncompressed build spanning two MemoryChunkSize chunks: 2 fetches", func(t *testing.T) {
		t.Parallel()
		h := fixture(t, bs, []header.BuildMap{
			{Offset: 0, Length: page, BuildId: buildA, BuildStorageOffset: 0},
			{Offset: page, Length: page, BuildId: buildA, BuildStorageOffset: storage.MemoryChunkSize + page},
			{Offset: 2 * page, Length: bs - 2*page, BuildId: uuid.Nil},
		}, nil)
		fm := gatherFetchmap(h, bs)
		require.Equal(t, []int{2}, fm.Cells)
	})
}

// TestCompressionPerChunk covers projection of self-frame compression ratios
// from U-space (storage offsets in self's data file) to V-space (virtual
// blocks) through SELF mappings. The U/V split — and the rule that only self
// frames count — was the bug that motivated this function.
func TestCompressionPerChunk(t *testing.T) {
	t.Parallel()

	const (
		bs   = 2 * miB
		page = 4096
	)
	selfID, ancestorID := uuid.New(), uuid.New()

	hdr := func(t *testing.T, size uint64, maps []header.BuildMap) *header.Header {
		t.Helper()

		return &header.Header{
			Metadata: &header.Metadata{Version: header.MetadataVersionV4, BlockSize: bs, Size: size, BuildId: selfID},
			Mapping:  testMapping(t, page, maps),
		}
	}
	// One 2 MiB frame at U=[0, 2MiB), ratio 4x; one at U=[2MiB, 4MiB), ratio 2x.
	frames := []frameInfo{
		{StartU: 0, EndU: bs, StartC: 0, EndC: bs / 4},           // 4.0x
		{StartU: bs, EndU: 2 * bs, StartC: bs / 4, EndC: bs - 1}, // ~2.66x (compressed denser at end)
	}

	t.Run("nil header or no frames returns all -1", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, []float64{-1, -1}, compressionPerChunk(nil, frames, 2, bs))
		h := hdr(t, 2*bs, []header.BuildMap{{Offset: 0, Length: 2 * bs, BuildId: selfID, BuildStorageOffset: 0}})
		require.Equal(t, []float64{-1, -1}, compressionPerChunk(h, nil, 2, bs))
	})

	t.Run("identity mapping projects frame to its same V-block", func(t *testing.T) {
		t.Parallel()
		// One full-size mapping: V[0,4MiB) == U[0,4MiB). Each block gets its frame's ratio.
		h := hdr(t, 2*bs, []header.BuildMap{
			{Offset: 0, Length: 2 * bs, BuildId: selfID, BuildStorageOffset: 0},
		})
		got := compressionPerChunk(h, frames, 2, bs)
		require.InDelta(t, 4.0, got[0], 1e-9, "block 0 = frame 0 ratio")
		require.InDelta(t, ratio(frames[1].EndU-frames[1].StartU, frames[1].EndC-frames[1].StartC), got[1], 1e-9)
	})

	t.Run("non-identity mapping projects U→V correctly", func(t *testing.T) {
		t.Parallel()
		// V-block 0 has nothing self. V-block 1 has self pages whose storage
		// offsets sit in frame 0 (U=[0, 2MiB)). So block 1 should get frame 0's
		// ratio (4.0x), not frame 1's.
		h := hdr(t, 2*bs, []header.BuildMap{
			{Offset: 0, Length: bs, BuildId: uuid.Nil},
			{Offset: bs, Length: bs, BuildId: selfID, BuildStorageOffset: 0},
		})
		got := compressionPerChunk(h, frames, 2, bs)
		require.Less(t, got[0], 0.0, "V-block 0 is sparse")
		require.InDelta(t, 4.0, got[1], 1e-9, "V-block 1 was mapped from U=[0,2MiB) → frame 0")
	})

	t.Run("ancestor-only mappings yield no compression cell", func(t *testing.T) {
		t.Parallel()
		// V-block 0 covered by ancestor mapping; block 1 sparse. Compression row
		// should be all -1 — we only show SELF frames.
		h := hdr(t, 2*bs, []header.BuildMap{
			{Offset: 0, Length: bs, BuildId: ancestorID, BuildStorageOffset: 0},
			{Offset: bs, Length: bs, BuildId: uuid.Nil},
		})
		got := compressionPerChunk(h, frames, 2, bs)
		require.Less(t, got[0], 0.0)
		require.Less(t, got[1], 0.0)
	})

	t.Run("multiple self frames in one V-block: worst (lowest) ratio wins", func(t *testing.T) {
		t.Parallel()
		// One 2 MiB V-block backed by two SELF half-mappings, each landing in a
		// different frame (one 4.0x, one ~2.66x). Worst wins.
		h := hdr(t, bs, []header.BuildMap{
			{Offset: 0, Length: bs / 2, BuildId: selfID, BuildStorageOffset: 0},
			{Offset: bs / 2, Length: bs / 2, BuildId: selfID, BuildStorageOffset: bs},
		})
		got := compressionPerChunk(h, frames, 1, bs)
		want := ratio(frames[1].EndU-frames[1].StartU, frames[1].EndC-frames[1].StartC)
		require.InDelta(t, want, got[0], 1e-9, "the worse of the two frames")
	})

	t.Run("self mapping whose U-range falls in a gap yields no cell", func(t *testing.T) {
		t.Parallel()
		// Frame table covers only U=[0,2MiB); a self mapping with storage at
		// U=8MiB has no covering frame — block stays sparse.
		h := hdr(t, bs, []header.BuildMap{
			{Offset: 0, Length: bs, BuildId: selfID, BuildStorageOffset: 8 * miB},
		})
		got := compressionPerChunk(h, frames[:1], 1, bs)
		require.Less(t, got[0], 0.0)
	})
}

// TestFramesInRange covers the --range filter for the per-frame view: a frame
// is included iff some self mapping with a V-overlap with the range also
// overlaps the frame's U-range.
func TestFramesInRange(t *testing.T) {
	t.Parallel()

	const bs = 2 * miB
	selfID := uuid.New()

	frames := []frameInfo{
		{StartU: 0, EndU: bs},
		{StartU: bs, EndU: 2 * bs},
		{StartU: 2 * bs, EndU: 3 * bs},
	}
	h := &header.Header{
		Metadata: &header.Metadata{Version: header.MetadataVersionV4, BlockSize: bs, Size: 3 * bs, BuildId: selfID},
		// Identity mapping covers V[0,6 MiB) → U[0, 6 MiB).
		Mapping: testMapping(t, 4096, []header.BuildMap{
			{Offset: 0, Length: 3 * bs, BuildId: selfID, BuildStorageOffset: 0},
		}),
	}

	t.Run("range unset returns all frames", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, frames, framesInRange(frames, h, selfID, span{}))
	})

	t.Run("range covers middle frame only", func(t *testing.T) {
		t.Parallel()
		got := framesInRange(frames, h, selfID, span{set: true, start: bs + 0x100, end: bs + 0x200})
		require.Len(t, got, 1)
		require.Equal(t, int64(bs), got[0].StartU)
	})

	t.Run("range straddles two frames", func(t *testing.T) {
		t.Parallel()
		got := framesInRange(frames, h, selfID, span{set: true, start: bs - 0x1000, end: bs + 0x1000})
		require.Len(t, got, 2)
		require.Equal(t, []int64{0, bs}, []int64{got[0].StartU, got[1].StartU})
	})
}
