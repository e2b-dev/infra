package storage

import (
	"encoding/binary"
	"errors"
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

// FrameTable is a decompression index for compressed diff files.
//
// Dirty blocks are grouped into frames and each frame is compressed
// independently. Two address spaces describe the same data:
//
//	U-space (uncompressed):  |-- frame 0 (2M) --|-- frame 1 (2M) --| ...
//	C-space (compressed):    |-- f0 (.6M) --|-- f1 (.7M) --| ...
//
// Each frame is a frameEntry with absolute offsets (StartU, StartC) and
// sizes (SizeU, SizeC). Lookups are a binary search on StartU.

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
// Nil-safe: returns 0 for nil (uncompressed leg in mixed-mode V4 upload).
func (ft *FrameTable) UncompressedSize() int64 {
	if ft == nil {
		return 0
	}

	var total int64
	for _, e := range ft.entries {
		total += int64(e.SizeU)
	}

	return total
}

// CompressedSize returns the total compressed size across all frames.
// Nil-safe: returns 0 for nil (uncompressed leg in mixed-mode V4 upload).
func (ft *FrameTable) CompressedSize() int64 {
	if ft == nil {
		return 0
	}

	var total int64
	for _, e := range ft.entries {
		total += int64(e.SizeC)
	}

	return total
}

// locate finds the frame containing the given uncompressed offset.
func (ft *FrameTable) locate(offset int64) (frameEntry, error) {
	if ft == nil {
		return frameEntry{}, errors.New("locate called with nil frame table — data is not compressed")
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

// LocateCompressed maps a U-space offset to its C-space byte range.
// This is the final step of the read path: after GetShiftedMapping resolves
// the virtual offset to a build-local U-offset, this locates the compressed
// bytes to fetch from storage.
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

	if n > 0 {
		if err := binary.Write(w, binary.LittleEndian, ft.entries); err != nil {
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
	if err := binary.Read(r, binary.LittleEndian, entries); err != nil {
		return nil, fmt.Errorf("read frame entries: %w", err)
	}

	for i := range entries {
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
// overlap with at least one of the given U-space byte ranges.
// Used during V4 header serialization to keep headers compact when a build
// has many frames but only a few are referenced in the current layer.
// Nil-safe: returns ft unchanged when ft is nil or ranges is empty.
func (ft *FrameTable) TrimToRanges(ranges []Range) *FrameTable {
	if ft == nil || len(ft.entries) == 0 || len(ranges) == 0 {
		return ft
	}

	keep := make([]bool, len(ft.entries))
	kept := 0

	for _, r := range ranges {
		startU, endU := r.Offset, r.Offset+int64(r.Length)

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
