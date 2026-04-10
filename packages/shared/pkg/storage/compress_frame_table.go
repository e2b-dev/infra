package storage

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

type CompressionType byte

const (
	CompressionNone = CompressionType(iota)
	CompressionZstd
	CompressionLZ4

	// maxDeserializedFrames caps the number of frames read from a serialized
	// FrameTable to prevent OOM from corrupted headers. 1M frames = 2 TiB
	// uncompressed at 2 MiB frame size.
	maxDeserializedFrames = 1024 * 1024
)

// # Compression: files, frames, and two address spaces (U/C)
//
// Dirty blocks (4 KiB rootfs, 2 MiB memfile) are packed into a diff file,
// grouped into frames (default 2 MiB), and each frame compressed independently.
// Two address spaces describe the same data:
//
//	U-space (uncompressed):  |-- frame 0 (2M) --|-- frame 1 (2M) --| ...
//	C-space (compressed):    |-- f0 (.6M) --|-- f1 (.7M) --| ...
//
// Each frame's position is recorded as a frameEntry with absolute offsets
// (StartU, StartC) and sizes (SizeU, SizeC). A FrameTable is a sorted
// slice of these entries — lookups are a single binary search on StartU.
//
// # Read path: virtual offset → build mapping → frame lookup → C-space fetch
//
// The header maps virtual offsets to builds via BuildMap entries. Each
// mapping says "virtual range [Offset, Offset+Length) lives in build X
// starting at BuildStorageOffset". The read path:
//
//  1. GetShiftedMapping(virtualOff) → {BuildId, U-offset within build, length}
//  2. header.GetBuildFrameData(BuildId) → build's FrameTable
//  3. ft.LocateCompressed(U-offset) → C-space byte range
//  4. Fetch compressed bytes from GCS, decompress, cache, return block
//
// # Build chain: how Builds propagates through layered templates
//
// Each V4 header carries a Builds map (Header.Builds) with per-build
// metadata: file size, checksum, and FrameTable. When a child template
// is built on a parent, ToDiffHeader merges mappings and copies the
// parent's Builds entries for all still-referenced build IDs. Then
// applyToHeader adds the current build's own entry:
//
//	base (build=aa)          → Builds: {aa: {ft, size, checksum}}
//	  └─ child (build=bb)    → Builds: {aa: ..., bb: ...}
//	       └─ grandchild (cc)→ Builds: {aa: ..., bb: ..., cc: ...}
//
// # Sparse trimming: serialization compacts frame tables
//
// During V4 header serialization, each build's FrameTable is trimmed to
// only the frames overlapping that build's mappings (TrimToRanges). This
// keeps headers compact when a build has many frames but only a few are
// referenced in the current layer.
//
// # V3/V4 interop: uncompressed layers in the chain
//
// V3 (uncompressed) headers have Builds == nil and do not serialize build
// metadata. A V3 layer in the middle of a chain drops all ancestor build
// metadata. The read path degrades gracefully: nil FrameTable means read
// uncompressed; missing build size falls back to a Size() RPC. In
// practice all layers use the same format (all V3 or all V4); mixed
// chains arise only during transitions.

// FrameSize holds the uncompressed (U) and compressed (C) byte size of a
// single frame.
type FrameSize struct {
	U int32
	C int32
}

func (s FrameSize) String() string {
	return fmt.Sprintf("U:%d/C:%d", s.U, s.C)
}

type Range struct {
	Offset int64
	Length int
}

func (r Range) String() string {
	return fmt.Sprintf("%d/%d", r.Offset, r.Length)
}

// frameEntry stores one frame as an absolute start offset plus size in both
// address spaces. Fields must be exported for encoding/binary (Read/Write use reflection).
// Field order chosen for optimal alignment: two int64 then two uint32 = 24 bytes, no padding.
type frameEntry struct {
	StartU int64
	StartC int64
	SizeU  int32
	SizeC  int32
}

// FrameTable is the decompression index for a compressed diff file.
// Immutable after construction; safe to share across goroutines.
// Sparse tables (gaps between entries) are supported.
type FrameTable struct {
	compressionType CompressionType
	entries         []frameEntry // sorted by StartU
}

// newFrameTableFromEntries creates a FrameTable from pre-computed absolute-offset entries.
func newFrameTableFromEntries(ct CompressionType, entries []frameEntry) *FrameTable {
	return &FrameTable{compressionType: ct, entries: entries}
}

// NewFrameTable creates a FrameTable from consecutive frame sizes, computing
// absolute offsets starting from zero.
func NewFrameTable(ct CompressionType, sizes []FrameSize) *FrameTable {
	if len(sizes) == 0 {
		return newFrameTableFromEntries(ct, nil)
	}

	entries := make([]frameEntry, len(sizes))

	var u, c int64
	for i, s := range sizes {
		entries[i] = frameEntry{
			StartU: u,
			StartC: c,
			SizeU:  s.U,
			SizeC:  s.C,
		}
		u += int64(s.U)
		c += int64(s.C)
	}

	return newFrameTableFromEntries(ct, entries)
}

// CompressionType returns the compression type. Nil-safe: returns CompressionNone for nil.
func (ft *FrameTable) CompressionType() CompressionType {
	if ft == nil {
		return CompressionNone
	}

	return ft.compressionType
}

// IsCompressed reports whether ft is non-nil and has a compression type set.
func (ft *FrameTable) IsCompressed() bool {
	return ft != nil && ft.compressionType != CompressionNone
}

func (ft *FrameTable) NumFrames() int {
	if ft == nil {
		return 0
	}

	return len(ft.entries)
}

func (e frameEntry) endU() int64 { return e.StartU + int64(e.SizeU) }
func (e frameEntry) endC() int64 { return e.StartC + int64(e.SizeC) }

func (ft *FrameTable) FrameAt(i int) (startU, endU, startC, endC int64) {
	e := ft.entries[i]

	return e.StartU, e.endU(), e.StartC, e.endC()
}

// UncompressedSize returns the total uncompressed size across all frames.
func (ft *FrameTable) UncompressedSize() int64 {
	var total int64
	for _, e := range ft.entries {
		total += int64(e.SizeU)
	}

	return total
}

// CompressedSize returns the total compressed size across all frames.
func (ft *FrameTable) CompressedSize() int64 {
	var total int64
	for _, e := range ft.entries {
		total += int64(e.SizeC)
	}

	return total
}

// locate finds the frame containing the given uncompressed offset.
func (ft *FrameTable) locate(offset int64) (frameEntry, error) {
	if ft == nil {
		return frameEntry{}, fmt.Errorf("locate called with nil frame table — data is not compressed")
	}

	// Binary search: find the last entry whose StartU <= offset.
	i := sort.Search(len(ft.entries), func(i int) bool {
		return ft.entries[i].StartU > offset
	}) - 1

	if i < 0 {
		return frameEntry{}, fmt.Errorf("offset %d not found in frame table", offset)
	}

	e := ft.entries[i]
	if offset >= e.endU() {
		return frameEntry{}, fmt.Errorf("offset %d is in a gap (not covered by any frame)", offset)
	}

	return e, nil
}

// LocateCompressed returns the compressed byte range for the frame containing
// the given uncompressed offset.
func (ft *FrameTable) LocateCompressed(offset int64) (Range, error) {
	e, err := ft.locate(offset)
	if err != nil {
		return Range{}, err
	}

	return Range{Offset: e.StartC, Length: int(e.SizeC)}, nil
}

// LocateUncompressed returns the uncompressed byte range for the frame
// containing the given uncompressed offset.
func (ft *FrameTable) LocateUncompressed(offset int64) (Range, error) {
	e, err := ft.locate(offset)
	if err != nil {
		return Range{}, err
	}

	return Range{Offset: e.StartU, Length: int(e.SizeU)}, nil
}

// Serialize writes the frame table to w in binary little-endian format.
// Nil-safe: writes zeros for type and count.
func (ft *FrameTable) Serialize(w io.Writer) error {
	var ct CompressionType
	var n int
	if ft != nil && ft.compressionType != CompressionNone {
		ct = ft.compressionType
		n = len(ft.entries)
	}

	if err := binary.Write(w, binary.LittleEndian, uint32(ct)); err != nil {
		return err
	}

	if err := binary.Write(w, binary.LittleEndian, uint32(n)); err != nil {
		return err
	}

	for i := range n {
		if err := binary.Write(w, binary.LittleEndian, ft.entries[i]); err != nil {
			return err
		}
	}

	return nil
}

// DeserializeFrameTable reads a FrameTable from r. Returns nil for
// uncompressed builds (compressionType=0 or numFrames=0).
func DeserializeFrameTable(r io.Reader) (*FrameTable, error) {
	var ct uint32

	if err := binary.Read(r, binary.LittleEndian, &ct); err != nil {
		return nil, fmt.Errorf("read compression type: %w", err)
	}

	var n uint32

	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("read frame count: %w", err)
	}

	if ct == 0 && n > 0 {
		return nil, fmt.Errorf("compression type is 0 but frame count is %d: corrupted header", n)
	}
	if ct == 0 || n == 0 {
		return nil, nil
	}

	if n > maxDeserializedFrames {
		return nil, fmt.Errorf("frame count %d exceeds maximum %d", n, maxDeserializedFrames)
	}

	entries := make([]frameEntry, n)
	for i := range n {
		if err := binary.Read(r, binary.LittleEndian, &entries[i]); err != nil {
			return nil, fmt.Errorf("read frame entry %d: %w", i, err)
		}
		if entries[i].SizeU <= 0 || entries[i].SizeC <= 0 {
			return nil, fmt.Errorf("frame %d has zero or negative size: SizeU=%d SizeC=%d", i, entries[i].SizeU, entries[i].SizeC)
		}
		if i > 0 && entries[i].StartU < entries[i-1].endU() {
			return nil, fmt.Errorf("frame %d StartU %d < previous endU %d: U-entries not sorted", i, entries[i].StartU, entries[i-1].endU())
		}
		if i > 0 && entries[i].StartC < entries[i-1].endC() {
			return nil, fmt.Errorf("frame %d StartC %d < previous endC %d: C-entries not sorted", i, entries[i].StartC, entries[i-1].endC())
		}
	}

	return newFrameTableFromEntries(CompressionType(ct), entries), nil
}

// TrimToRanges returns a new FrameTable containing only the frames that
// overlap with at least one of the given [startU, endU) byte ranges.
func (ft *FrameTable) TrimToRanges(ranges [][2]int64) *FrameTable {
	if ft == nil || len(ft.entries) == 0 || len(ranges) == 0 {
		return ft
	}

	keep := make([]bool, len(ft.entries))
	kept := 0

	for _, r := range ranges {
		startU, endU := r[0], r[1]

		// Binary search: first frame whose EndU > startU.
		lo := sort.Search(len(ft.entries), func(i int) bool {
			return ft.entries[i].endU() > startU
		})

		for i := lo; i < len(ft.entries) && ft.entries[i].StartU < endU; i++ {
			if !keep[i] {
				keep[i] = true
				kept++
			}
		}
	}

	if kept == len(ft.entries) {
		return ft // nothing trimmed
	}

	trimmed := make([]frameEntry, 0, kept)
	for i, e := range ft.entries {
		if keep[i] {
			trimmed = append(trimmed, e)
		}
	}

	return newFrameTableFromEntries(ft.compressionType, trimmed)
}

func (ct CompressionType) Suffix() string {
	switch ct {
	case CompressionZstd:
		return ".zstd"
	case CompressionLZ4:
		return ".lz4"
	default:
		return ""
	}
}

func (ct CompressionType) String() string {
	switch ct {
	case CompressionZstd:
		return "zstd"
	case CompressionLZ4:
		return "lz4"
	default:
		return "none"
	}
}

// parseCompressionType converts a string to CompressionType.
// Returns CompressionNone for unrecognised values.
func parseCompressionType(s string) CompressionType {
	switch s {
	case "lz4":
		return CompressionLZ4
	case "zstd":
		return CompressionZstd
	default:
		return CompressionNone
	}
}
