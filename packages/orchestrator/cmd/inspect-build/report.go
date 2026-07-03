package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// report is the inspected build, gathered once and rendered per mode. Detail
// payloads (Mappings.List, Data.Frames, Image.Metadata) are populated by gather
// and dropped by filterReport unless their section is expanded.
type report struct {
	Source   string          `json:"source"`
	Artifact string          `json:"artifact"`
	Header   headerInfo      `json:"header"`
	Image    imageInfo       `json:"image"`
	Mappings mappingsSection `json:"mappings"`
	Builds   []buildInfo     `json:"builds"`
	// Data is an internal self-summary used by the human renderer; everything
	// it carries is already in builds[self], so it is not serialized.
	Data dataSection `json:"-"`
	// Usage measures how much of this build a --recursive head draws on; set
	// only on ancestor reports.
	Usage *ancestorUsage `json:"usage,omitempty"`
	// Fetchmap is the per-chunk fetch-fanout map. Rendered as the bottom row
	// of the FRAMEMAP visualization. Not serialized — it's a derivable index
	// (mappings + each build's frame table give the same info, and builds[]
	// now carries those frame tables).
	Fetchmap *fetchmap `json:"-"`

	h *header.Header // not serialized
}

// fetchmap is the per-chunk read-segment density map. The virtual address
// space is divided into fixed-size chunks (typically 2 MiB, the frame and
// hugepage size). For each chunk, counts the distinct backing fetches a cold
// restore would issue to fill it: a maximal run of contiguous same-BuildId
// mappings whose BuildStorageOffsets continue the previous mapping's stream
// collapses into one segment; anything that breaks that run (a different
// BuildId, or a non-adjacent storage offset) starts a new one. Excludes
// uuid.Nil (sparse zero-fill, served as zeros without a fetch). Chunks are
// the visualization unit, distinct from device-blocks (Metadata.BlockSize)
// and from stored compression frames (Data.Frames). This is the
// fragmentation metric memfile dedup density work cares about — see #2862.
type fetchmap struct {
	ChunkSize     int64   `json:"chunk_size"`      // bytes per chunk
	ChunkCount    int     `json:"chunk_count"`     // ceil(image_size / chunk_size)
	TouchedChunks int     `json:"touched_chunks"`  // chunks with at least one non-Nil mapping
	MaxSegments   int     `json:"max_segments"`    // worst chunk's distinct fetch-segment count
	AvgSegments   float64 `json:"avg_segments"`    // mean fetch segments over touched chunks
	MaxLayers     int     `json:"max_layers"`      // worst chunk's distinct ancestor build count
	AvgLayers     float64 `json:"avg_layers"`      // mean ancestor build count over touched chunks
	MaxChunkOff   int64   `json:"max_chunk_off"`   // virtual offset of the first MaxSegments chunk
	Cells         []int   `json:"cells,omitempty"` // populated only when expanded; len == ChunkCount; cell = # fetch segments
}

type headerInfo struct {
	Version            uint64  `json:"version"`
	HeaderSize         int64   `json:"header_size"`                   // on-disk header bytes
	HeaderUncompressed int64   `json:"header_uncompressed,omitempty"` // V4+: header size if its LZ4 payload were inflated
	HeaderRatio        float64 `json:"header_ratio,omitempty"`        // HeaderUncompressed / HeaderSize, when LZ4-compressed
	StorageSize        int64   `json:"storage_size"`                  // stored (possibly compressed) data-file bytes
	Ratio              float64 `json:"ratio,omitempty"`               // data-file uncompressed / storage size, when compressed
}

type imageInfo struct {
	VirtualSize uint64    `json:"virtual_size"`
	DiffSize    int64     `json:"diff_size"` // this layer's own uncompressed bytes
	BuildID     uuid.UUID `json:"build_id"`
	BaseBuildID uuid.UUID `json:"base_build_id"`
	Ancestors   int       `json:"ancestors"`
	FromImage   string    `json:"from_image,omitempty"`
	Kernel      string    `json:"kernel,omitempty"`
	Firecracker string    `json:"firecracker,omitempty"`
	User        string    `json:"user,omitempty"`
	// Metadata is the full metadata.json — populated only when expanded.
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type mappingsSection struct {
	Count   int           `json:"count"`
	ByBuild []buildExtent `json:"by_build"`
	List    []mapping     `json:"list,omitempty"` // populated only when expanded
}

type buildExtent struct {
	BuildID  uuid.UUID `json:"build_id"`
	Role     string    `json:"role"`
	Bytes    uint64    `json:"bytes"`
	Mappings int       `json:"mappings"`
}

type mapping struct {
	Offset        uint64    `json:"offset"`
	Length        uint64    `json:"length"`
	BuildID       uuid.UUID `json:"build_id"`
	Role          string    `json:"role"`
	StorageOffset uint64    `json:"storage_offset"`
}

type buildInfo struct {
	BuildID          uuid.UUID `json:"build_id"`
	Role             string    `json:"role"`
	Compression      string    `json:"compression"`
	UncompressedSize int64     `json:"uncompressed_size"`
	CompressedSize   int64     `json:"compressed_size,omitempty"`
	Ratio            float64   `json:"ratio,omitempty"`
	FrameCount       int       `json:"frame_count,omitempty"`
	// Frames is the build's frame table when known. Populated for V4+
	// headers with this build present in Builds. Only the self build's
	// frames carry the Fetches field; ancestor entries are bare tables.
	Frames   []frameInfo `json:"frames,omitempty"`
	Checksum string      `json:"checksum"`
}

type dataSection struct {
	Compressed       bool        `json:"compressed"`
	CompressionType  string      `json:"compression_type"`
	UncompressedSize int64       `json:"uncompressed_size"`
	CompressedSize   int64       `json:"compressed_size,omitempty"`
	Ratio            float64     `json:"ratio,omitempty"`
	FrameCount       int         `json:"frame_count"`
	Frames           []frameInfo `json:"frames,omitempty"` // populated only when expanded
}

type frameInfo struct {
	StartU int64 `json:"start_u"`
	EndU   int64 `json:"end_u"`
	StartC int64 `json:"start_c"`
	EndC   int64 `json:"end_c"`
	// Fetches is the cold-restore fetch count for the V-chunk aligned with
	// this frame's U position (assumes identity-ish mapping at the chunk
	// granularity — exact for non-deduped builds). One fetch is one frame
	// from one build's storage; 1 here means "just this frame". Set only
	// for self's frames; ancestor frames carry just the bare frame table.
	Fetches *int `json:"fetches,omitempty"`
}

// metadataSummary is the subset of metadata.json surfaced as scalar fields.
type metadataSummary struct {
	FromImage string `json:"from_image"`
	Template  struct {
		KernelVersion      string `json:"kernel_version"`
		FirecrackerVersion string `json:"firecracker_version"`
	} `json:"template"`
	Context struct {
		User string `json:"user"`
	} `json:"context"`
}

// ancestorUsage measures how much of an ancestor build the recursive target
// draws on: the uncompressed bytes and frames its mappings actually reach.
type ancestorUsage struct {
	UsedBytes     int64   `json:"used_bytes"`     // union of ancestor offsets the target maps
	DiffBytes     int64   `json:"diff_bytes"`     // ancestor's full uncompressed data
	UsedFraction  float64 `json:"used_fraction"`  // UsedBytes / DiffBytes
	Mappings      int     `json:"mappings"`       // target-build mappings into this ancestor
	FramesTouched int     `json:"frames_touched"` // ancestor frames the target must fetch
	TotalFrames   int     `json:"total_frames"`   // ancestor's full frame count
}

const (
	roleCurrent  = "current"
	roleParent   = "parent"
	roleAncestor = "ancestor"
	roleZero     = "zero"
)

// fetchmapChunkSize is the FRAMEMAP cell size: 2 MiB. It matches the default
// compressed frame size, the x86_64 huge-page size, and the production
// chunker's per-page-fault fetch unit for compressed reads. Distinct from
// device blocks (Metadata.BlockSize) and from stored compression frames
// (Data.Frames); a chunk is a virtual address-space region.
const fetchmapChunkSize = 2 * 1024 * 1024

// roleOf classifies a build ID relative to the inspected header.
func roleOf(id uuid.UUID, meta *header.Metadata) string {
	switch id {
	case uuid.Nil:
		return roleZero
	case meta.BuildId:
		return roleCurrent
	case meta.BaseBuildId:
		return roleParent
	default:
		return roleAncestor
	}
}

// gather loads a build's header, metadata, and stored size into a full report.
func gather(ctx context.Context, storagePath, buildID, artifact string) (*report, error) {
	headerData, source, err := cmdutil.ReadFile(ctx, storagePath, buildID, artifact+storage.HeaderSuffix)
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	h, err := header.DeserializeBytes(headerData)
	if err != nil {
		return nil, fmt.Errorf("deserialize header: %w", err)
	}

	hdr := headerInfo{Version: h.Metadata.Version, HeaderSize: int64(len(headerData))}
	if u, err := headerUncompressedSize(headerData, h.Metadata.Version); err == nil {
		hdr.HeaderUncompressed = u
		if u > hdr.HeaderSize {
			hdr.HeaderRatio = ratio(u, hdr.HeaderSize)
		}
	}

	r := &report{
		Source:   source,
		Artifact: artifact,
		h:        h,
		Header:   hdr,
		Image: imageInfo{
			VirtualSize: h.Metadata.Size,
			BuildID:     h.Metadata.BuildId,
			BaseBuildID: h.Metadata.BaseBuildId,
		},
	}

	currentFrames := h.GetBuildFrameData(h.Metadata.BuildId)
	r.Header.StorageSize, err = storedSize(ctx, storagePath, buildID, artifact, currentFrames)
	if err != nil {
		// The header inspects fine without the data file — degrade rather
		// than abort, so a build whose data was GC'd is still inspectable.
		fmt.Fprintf(os.Stderr, "warning: %s\n", err)
		r.Header.StorageSize = sizeUnknown
	}
	r.Data = gatherData(h, currentFrames, r.Header.StorageSize)
	r.Image.DiffSize = r.Data.UncompressedSize
	if r.Data.Compressed {
		r.Header.Ratio = ratio(r.Data.UncompressedSize, r.Header.StorageSize)
	}
	r.Mappings, r.Image.Ancestors = gatherMappings(h)
	r.Builds = gatherBuilds(h, r.Header.StorageSize)
	r.Fetchmap = gatherFetchmap(h, fetchmapChunkSize)
	// Self's frames live in two places — r.Data.Frames (the rendering path)
	// and r.Builds[self].Frames (the JSON contract). Annotate both.
	annotateFrameFetches(r.Data.Frames, r.Fetchmap, fetchmapChunkSize)
	for i := range r.Builds {
		if r.Builds[i].BuildID == h.Metadata.BuildId {
			annotateFrameFetches(r.Builds[i].Frames, r.Fetchmap, fetchmapChunkSize)
		}
	}

	if meta, _, err := cmdutil.ReadFile(ctx, storagePath, buildID, storage.MetadataName); err == nil {
		r.Image.Metadata = json.RawMessage(meta)
		var m metadataSummary
		if uErr := json.Unmarshal(meta, &m); uErr != nil {
			fmt.Fprintf(os.Stderr, "warning: metadata.json is not valid JSON: %s\n", uErr)
		} else {
			r.Image.FromImage = m.FromImage
			r.Image.Kernel = m.Template.KernelVersion
			r.Image.Firecracker = m.Template.FirecrackerVersion
			r.Image.User = m.Context.User
		}
	}

	return r, nil
}

// gatherChain gathers the target build and, when recursive is set, every
// transitive ancestor. The result is dependency-ordered: the target build
// first, then its parent and ancestors, nearer ones before farther ones.
func gatherChain(ctx context.Context, storagePath, buildID, artifact string, recursive bool) ([]*report, error) {
	head, err := gather(ctx, storagePath, buildID, artifact)
	if err != nil {
		return nil, err
	}

	chain := []*report{head}
	if !recursive {
		return chain, nil
	}

	// Breadth-first over the ancestor graph; the chain itself is the queue, so
	// nearer ancestors are gathered (and listed) before farther ones.
	seen := map[uuid.UUID]bool{head.Image.BuildID: true}
	for i := 0; i < len(chain); i++ {
		for _, id := range ancestorIDs(chain[i]) {
			if seen[id] {
				continue
			}
			seen[id] = true

			anc, err := gather(ctx, storagePath, id.String(), artifact)
			if err != nil {
				// A missing ancestor is skipped rather than failing the whole
				// inspection — the rest of the chain still inspects fine.
				fmt.Fprintf(os.Stderr, "warning: skipping ancestor %s: %s\n", id, err)

				continue
			}
			anc.Usage = ancestorUsageOf(head, anc)
			chain = append(chain, anc)
		}
	}

	return chain, nil
}

// ancestorIDs returns the distinct builds r descends from: every build its
// mappings reference, plus its BaseBuildId — excluding r itself and the zero
// build.
func ancestorIDs(r *report) []uuid.UUID {
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{r.Image.BuildID: true, uuid.Nil: true}
	add := func(id uuid.UUID) {
		if seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}

	for _, e := range r.Mappings.ByBuild {
		add(e.BuildID)
	}
	add(r.Image.BaseBuildID)

	return ids
}

// ancestorUsageOf measures how much of ancestor anc the head build draws on,
// from head's mappings into anc and anc's own frame table.
func ancestorUsageOf(head, anc *report) *ancestorUsage {
	u := &ancestorUsage{
		DiffBytes:   anc.Data.UncompressedSize,
		TotalFrames: anc.Data.FrameCount,
	}

	var refs []byteRange
	for _, m := range head.Mappings.List {
		if m.BuildID != anc.Image.BuildID {
			continue
		}
		u.Mappings++
		refs = append(refs, byteRange{int64(m.StorageOffset), int64(m.StorageOffset + m.Length)})
	}

	used := mergeRanges(refs)
	for _, r := range used {
		u.UsedBytes += r.hi - r.lo
	}
	if u.DiffBytes > 0 {
		u.UsedFraction = float64(u.UsedBytes) / float64(u.DiffBytes)
	}
	for _, f := range anc.Data.Frames {
		if intersectsAny(used, byteRange{f.StartU, f.EndU}) {
			u.FramesTouched++
		}
	}

	return u
}

// annotateFrameFetches sets each self frame's Fetches field to the fetchmap
// cell at the V-chunk aligned with the frame's StartU. This is exact in the
// common (non-deduped, identity-mapped) case where a frame's U-position equals
// its backed V-position. For heavily-deduped builds the alignment is best-
// effort, since self frames pack pages from various V offsets.
func annotateFrameFetches(frames []frameInfo, fm *fetchmap, chunkSize uint64) {
	if fm == nil || len(fm.Cells) == 0 || chunkSize == 0 {
		return
	}
	for i := range frames {
		ci := int(uint64(frames[i].StartU) / chunkSize)
		if ci < 0 || ci >= len(fm.Cells) {
			continue
		}
		n := fm.Cells[ci]
		frames[i].Fetches = &n
	}
}

// framesInRange returns the subset of frames whose U-range is touched by any
// of buildID's mappings whose V-range overlaps rng. Used to filter the per-
// frame view when --range is set, per build. O(mappings log frames + frames).
func framesInRange(frames []frameInfo, h *header.Header, buildID uuid.UUID, rng span) []frameInfo {
	if !rng.set || len(frames) == 0 {
		return frames
	}
	hit := make([]bool, len(frames))
	for _, m := range h.Mapping.All() {
		if m.BuildId != buildID || m.Length == 0 {
			continue
		}
		vLo := max(m.Offset, rng.start)
		vHi := min(m.Offset+m.Length, rng.end)
		if vLo >= vHi {
			continue
		}
		uLo := int64(m.BuildStorageOffset + (vLo - m.Offset))
		uHi := int64(m.BuildStorageOffset + (vHi - m.Offset))
		fi := sort.Search(len(frames), func(i int) bool {
			return frames[i].EndU > uLo
		})
		for ; fi < len(frames) && frames[fi].StartU < uHi; fi++ {
			hit[fi] = true
		}
	}
	out := make([]frameInfo, 0, len(frames))
	for i, f := range frames {
		if hit[i] {
			out = append(out, f)
		}
	}

	return out
}

func gatherData(h *header.Header, ft *storage.FrameTable, storedSize int64) dataSection {
	d := dataSection{
		Compressed:      ft.IsCompressed(),
		CompressionType: ft.CompressionType().String(),
	}

	if !ft.IsCompressed() {
		// V4+ stores the uncompressed size per build; V3 has no Builds map, so
		// the data file itself is the uncompressed data.
		if bd, ok := h.Builds[h.Metadata.BuildId]; ok {
			d.UncompressedSize = bd.Size
		} else {
			d.UncompressedSize = storedSize
		}

		return d
	}

	// Canonical sizes: Builds[self].Size is the recorded uncompressed image
	// size, the on-disk storedSize is the compressed file's bytes. ft.*Size()
	// would be smaller when the self frame table is sparse-trimmed (V4+ can
	// drop frames while preserving original U offsets).
	if bd, ok := h.Builds[h.Metadata.BuildId]; ok && bd.Size > 0 {
		d.UncompressedSize = bd.Size
	} else {
		d.UncompressedSize = ft.UncompressedSize()
	}
	d.CompressedSize = storedSize
	d.Ratio = ratio(d.UncompressedSize, d.CompressedSize)
	d.FrameCount = ft.NumFrames()
	for i := range d.FrameCount {
		startU, endU, startC, endC := ft.FrameAt(i)
		d.Frames = append(d.Frames, frameInfo{StartU: startU, EndU: endU, StartC: startC, EndC: endC})
	}

	return d
}

func gatherMappings(h *header.Header) (mappingsSection, int) {
	sec := mappingsSection{Count: h.Mapping.Len()}

	totals := map[uuid.UUID]uint64{}
	counts := map[uuid.UUID]int{}
	var order []uuid.UUID
	ancestors := map[uuid.UUID]struct{}{}
	for _, m := range h.Mapping.All() {
		if _, seen := totals[m.BuildId]; !seen {
			order = append(order, m.BuildId)
		}
		totals[m.BuildId] += m.Length
		counts[m.BuildId]++
		sec.List = append(sec.List, mapping{
			Offset:        m.Offset,
			Length:        m.Length,
			BuildID:       m.BuildId,
			Role:          roleOf(m.BuildId, h.Metadata),
			StorageOffset: m.BuildStorageOffset,
		})
		if m.BuildId != uuid.Nil && m.BuildId != h.Metadata.BuildId {
			ancestors[m.BuildId] = struct{}{}
		}
	}
	for _, id := range order {
		sec.ByBuild = append(sec.ByBuild, buildExtent{
			BuildID: id, Role: roleOf(id, h.Metadata), Bytes: totals[id], Mappings: counts[id],
		})
	}
	rolePriority := map[string]int{roleCurrent: 0, roleParent: 1, roleAncestor: 2, roleZero: 3}
	slices.SortStableFunc(sec.ByBuild, func(a, b buildExtent) int {
		return rolePriority[a.Role] - rolePriority[b.Role]
	})

	return sec, len(ancestors)
}

// gatherFetchmap computes the per-chunk cold-restore fetch count by exactly
// mirroring block.Chunker.locateChunk: for each mapping covering a chunk,
// the fetch unit is the *frame containing the mapping's storage offset*
// (compressed) or the MemoryChunkSize-aligned chunk (uncompressed). Each
// chunk's cell value is the count of distinct (build, frame_index) tuples
// needed; mappings into the same frame share a fetch.
//
// chunkSize is the unit of one cell — a 2 MiB hugepage by default.
func gatherFetchmap(h *header.Header, chunkSize uint64) *fetchmap {
	if chunkSize == 0 || h.Metadata.Size == 0 {
		return nil
	}
	chunkCount := int((h.Metadata.Size + chunkSize - 1) / chunkSize)

	// fetchKey identifies a unique cold-restore fetch.
	//   Compressed builds: build + frame index in that build's frame table.
	//   Uncompressed builds: build + MemoryChunkSize-aligned chunk index,
	//     encoded as a negative value to avoid collision with frame indices.
	type fetchKey struct {
		build uuid.UUID
		idx   int64
	}
	fetches := make([]map[fetchKey]struct{}, chunkCount)
	layers := make([]map[uuid.UUID]struct{}, chunkCount)

	addFetch := func(c int, k fetchKey) {
		if fetches[c] == nil {
			fetches[c] = map[fetchKey]struct{}{}
		}
		fetches[c][k] = struct{}{}
		if layers[c] == nil {
			layers[c] = map[uuid.UUID]struct{}{}
		}
		layers[c][k.build] = struct{}{}
	}

	for _, m := range h.Mapping.All() {
		if m.BuildId == uuid.Nil || m.Length == 0 {
			continue // sparse zero-fill: no fetch
		}
		first := int(m.Offset / chunkSize)
		last := int((m.Offset + m.Length - 1) / chunkSize)
		if last >= chunkCount {
			last = chunkCount - 1
		}

		ft := h.GetBuildFrameData(m.BuildId)
		compressed := ft.IsCompressed()

		for c := first; c <= last; c++ {
			cLo := uint64(c) * chunkSize
			cHi := cLo + chunkSize
			vLo := max(m.Offset, cLo)
			vHi := min(m.Offset+m.Length, cHi)
			uLo := m.BuildStorageOffset + (vLo - m.Offset)
			uHi := m.BuildStorageOffset + (vHi - m.Offset)

			if compressed {
				// Binary search for first frame whose EndU > uLo, then walk
				// forward while StartU < uHi.
				lo, hi := 0, ft.NumFrames()
				for lo < hi {
					mid := (lo + hi) / 2
					_, endU, _, _ := ft.FrameAt(mid)
					if endU > int64(uLo) {
						hi = mid
					} else {
						lo = mid + 1
					}
				}
				for fi := lo; fi < ft.NumFrames(); fi++ {
					startU, _, _, _ := ft.FrameAt(fi)
					if startU >= int64(uHi) {
						break
					}
					addFetch(c, fetchKey{build: m.BuildId, idx: int64(fi)})
				}
			} else {
				// Uncompressed: production fetches MemoryChunkSize-aligned chunks.
				const mc = uint64(storage.MemoryChunkSize)
				chFirst := uLo / mc
				chLast := (uHi - 1) / mc
				for ch := chFirst; ch <= chLast; ch++ {
					// Negate so uncompressed chunk indices don't collide with frame indices.
					addFetch(c, fetchKey{build: m.BuildId, idx: -int64(ch) - 1})
				}
			}
		}
	}

	cells := make([]int, chunkCount)
	var (
		segSum, layerSum int
		touched          int
		maxSegments      int
		maxLayers        int
		maxOff           int64
	)
	for c := range chunkCount {
		n := 0
		if fetches[c] != nil {
			n = len(fetches[c])
		}
		cells[c] = n
		if n == 0 {
			continue
		}
		touched++
		segSum += n
		ls := len(layers[c])
		layerSum += ls
		if n > maxSegments {
			maxSegments = n
			maxOff = int64(c) * int64(chunkSize)
		}
		if ls > maxLayers {
			maxLayers = ls
		}
	}
	fm := &fetchmap{
		ChunkSize:     int64(chunkSize),
		ChunkCount:    chunkCount,
		TouchedChunks: touched,
		MaxSegments:   maxSegments,
		MaxLayers:     maxLayers,
		MaxChunkOff:   maxOff,
		Cells:         cells,
	}
	if touched > 0 {
		fm.AvgSegments = float64(segSum) / float64(touched)
		fm.AvgLayers = float64(layerSum) / float64(touched)
	}

	return fm
}

func gatherBuilds(h *header.Header, selfStoredSize int64) []buildInfo {
	builds := make([]buildInfo, 0, len(h.Builds))
	for id, bd := range h.Builds {
		b := buildInfo{
			BuildID:          id,
			Role:             roleOf(id, h.Metadata),
			Compression:      bd.FrameData.CompressionType().String(),
			UncompressedSize: bd.Size,
			Checksum:         checksumString(bd.Checksum),
		}
		if bd.FrameData.IsCompressed() {
			// Canonical uncompressed size is bd.Size (set above). The header
			// has no recorded compressed size, so only self's CompressedSize
			// is known here (from the on-disk storedSize); ancestor entries
			// stay zero and the renderer omits the line.
			if id == h.Metadata.BuildId && selfStoredSize > 0 {
				b.CompressedSize = selfStoredSize
				b.Ratio = ratio(b.UncompressedSize, b.CompressedSize)
			}
			b.FrameCount = bd.FrameData.NumFrames()
			b.Frames = make([]frameInfo, b.FrameCount)
			for i := range b.Frames {
				startU, endU, startC, endC := bd.FrameData.FrameAt(i)
				b.Frames[i] = frameInfo{StartU: startU, EndU: endU, StartC: startC, EndC: endC}
			}
		}
		builds = append(builds, b)
	}

	slices.SortFunc(builds, func(a, b buildInfo) int {
		return strings.Compare(a.BuildID.String(), b.BuildID.String())
	})

	return builds
}

// filterReport returns a copy with detail payloads dropped for sections that
// are not expanded, and the offset:size filters applied to those that are —
// the output phase (between gather and render) for the JSON renderer.
func filterReport(r *report, vw view) *report {
	out := *r

	if vw.expanded(sectionMappings) {
		out.Mappings.List = filteredMappings(r.Mappings.List, vw.rng)
	} else {
		out.Mappings.List = nil
	}

	// Each build's frame table is gated on -expand=frames; with -range, each
	// build's frames are filtered by V→U projection through THAT build's
	// mappings (so an ancestor's frames are filtered by ancestor mappings).
	if len(r.Builds) > 0 {
		out.Builds = make([]buildInfo, len(r.Builds))
		copy(out.Builds, r.Builds)
		for i := range out.Builds {
			if !vw.expanded(sectionFrames) {
				out.Builds[i].Frames = nil
			} else {
				out.Builds[i].Frames = framesInRange(out.Builds[i].Frames, r.h, out.Builds[i].BuildID, vw.rng)
			}
		}
	}

	if !vw.expanded(sectionMetadata) {
		out.Image.Metadata = nil
	}

	return &out
}

func filteredMappings(list []mapping, s span) []mapping {
	if !s.set {
		return list
	}
	var out []mapping
	for _, m := range list {
		if s.overlaps(m.Offset, m.Offset+m.Length) {
			out = append(out, m)
		}
	}

	return out
}

func filteredFrames(list []frameInfo, s span) []frameInfo {
	if !s.set {
		return list
	}
	var out []frameInfo
	for _, f := range list {
		if s.overlaps(uint64(f.StartU), uint64(f.EndU)) {
			out = append(out, f)
		}
	}

	return out
}

func storedSize(ctx context.Context, storagePath, buildID, artifact string, currentFrames *storage.FrameTable) (int64, error) {
	dataFile := artifact
	if currentFrames.IsCompressed() {
		dataFile += currentFrames.CompressionType().Suffix()
	}

	reader, size, _, err := cmdutil.OpenDataFile(ctx, storagePath, buildID, dataFile)
	if err != nil {
		return 0, fmt.Errorf("open data file %s: %w", dataFile, err)
	}
	reader.Close()

	return size, nil
}

// sizeUnknown marks a size that couldn't be determined (e.g. the data file is
// missing); renderers show it as "unknown".
const sizeUnknown = -1

func ratio(uncompressed, compressed int64) float64 {
	if compressed <= 0 {
		return 0
	}

	return float64(uncompressed) / float64(compressed)
}

// headerUncompressedSize returns the size a V4+ header would have if its LZ4
// payload were expanded. Returns len(data) for V3. Reads the on-disk size
// prefix without running a full deserialize.
//
// V4+ layout (see header/serialization_v4.go serializeV4 doc):
//
//	[Metadata] [uint8 flags] [uint32 uncompressedPayloadSize] [LZ4(payload)]
func headerUncompressedSize(data []byte, version uint64) (int64, error) {
	if version < header.MetadataVersionV4 {
		return int64(len(data)), nil
	}
	const (
		flagsLen  = 1
		sizeLen   = 4
		sizeStart = flagsLen // size prefix starts immediately after the flags byte
	)
	metaSize := binary.Size(header.Metadata{})
	if len(data) < metaSize+flagsLen+sizeLen {
		return 0, errors.New("v4+ header truncated before size prefix")
	}
	payload := int64(binary.LittleEndian.Uint32(data[metaSize+sizeStart:]))

	return int64(metaSize) + flagsLen + sizeLen + payload, nil
}

// checksumString renders a SHA-256 digest, or "" when unknown (zero value).
func checksumString(cs [32]byte) string {
	if cs == ([32]byte{}) {
		return ""
	}

	return "sha256:" + hex.EncodeToString(cs[:])
}

// buildInfoFor returns the header's own record for build id, when it references
// it — the trimmed view of how much of that build the header draws on.
func (r *report) buildInfoFor(id uuid.UUID) (buildInfo, bool) {
	for _, b := range r.Builds {
		if b.BuildID == id {
			return b, true
		}
	}

	return buildInfo{}, false
}
