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
)

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

// NewFrameTable creates a FrameTable from consecutive frame sizes, computing
// absolute offsets starting from zero.
func NewFrameTable(ct CompressionType, sizes []FrameSize) *FrameTable {
	if len(sizes) == 0 {
		return &FrameTable{compressionType: ct}
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

	return &FrameTable{compressionType: ct, entries: entries}
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

	if i < 0 || i >= len(ft.entries) {
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
	if !ft.IsCompressed() {
		z := [8]byte{} // two uint32 zeros: CompressionType=0, NumFrames=0
		_, err := w.Write(z[:])

		return err
	}

	if err := binary.Write(w, binary.LittleEndian, uint32(ft.compressionType)); err != nil {
		return err
	}

	if err := binary.Write(w, binary.LittleEndian, uint32(len(ft.entries))); err != nil {
		return err
	}

	for _, e := range ft.entries {
		if err := binary.Write(w, binary.LittleEndian, e); err != nil {
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

	// Cap to prevent OOM from corrupted headers. 1<<20 frames × 24 bytes = 24 MiB.
	const maxFrames = 1 << 20
	if n > maxFrames {
		return nil, fmt.Errorf("frame count %d exceeds maximum %d", n, maxFrames)
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

	return &FrameTable{compressionType: CompressionType(ct), entries: entries}, nil
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

	return &FrameTable{compressionType: ft.compressionType, entries: trimmed}
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
