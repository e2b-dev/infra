package storage

import (
	"fmt"
)

type CompressionType byte

const (
	CompressionNone = CompressionType(iota)
	CompressionZstd
	CompressionLZ4
)

// # Compression Layout
//
// Dirty blocks (4 KiB rootfs, 2 MiB memfile) are packed into a diff file,
// grouped into frames (default 2 MiB), and each frame compressed independently.
// Two address spaces describe the same data:
//
//     |blk|...|blk | blk|...|blk |
//     |- f0 (2M) - | ---  f1 --- |
//	U: 0             2M            4M
//     |- f0 -|- f1 -|- f2 -|
//	C: 0     .6M    1.3M   1.7M
//
// # BuildMaps and FrameTable Subsets
//
// The header maps virtual offsets to builds via BuildMap entries. Each
// mapping carries a FrameTable subset covering just its byte range:
//
//	Virtual:  ──[━━━━ M0 ━━━━]──[━━━━━━ M1 ━━━━━━]──[━━ M2 ━━]──
//	            BuildId=aa         BuildId=bb            BuildId=aa
//	            BuildStorageOffset=0  BuildStorageOffset=0  BuildStorageOffset=8M
//	            Length=4M          Length=6M            Length=2M
//	            ft: frames 0-1  ft: frames 0-2       ft: frames 4-4
//
// # Read Path: Virtual Offset → C-Space Fetch
//
//	read virtual offset V=5M
//	  │
//	  ├─ find mapping: M1 {Build=bb, BuildStorageOffset=0, Length=6M}
//	  │
//	  ├─ diff offset = V - M1.Offset + M1.BuildStorageOffset = 5M
//	  │
//	  ├─ M1.ft: frame 0: U:[0,2M)  frame 1: U:[2M,4M)  frame 2: U:[4M,6M)
//    │  LocateCompressed(5M) → frame2, C:[2.0M, 3.3M)
//	  ├─ fetch C bytes [2.0M, 3.3M) from build "bb"
//	  ├─ decompress → 2 MiB frame, cache
//	  └─ return block at offset 5M - 4M = 1M within frame

// FrameOffset holds a position in both address spaces.
// U is the byte offset in the uncompressed diff file.
// C is the byte offset in the compressed diff file.
type FrameOffset struct {
	U int64
	C int64
}

func (o *FrameOffset) String() string {
	return fmt.Sprintf("U:%d/C:%d", o.U, o.C)
}

func (o *FrameOffset) Add(f FrameSize) {
	o.U += int64(f.U)
	o.C += int64(f.C)
}

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

// FrameTable is the decompression index for a compressed diff file (or a
// contiguous subset of one). Offset is the position of the first frame in
// both address spaces; Frames lists each frame's U and C sizes in order.
type FrameTable struct {
	compressionType CompressionType
	Offset          FrameOffset
	Frames          []FrameSize
}

// NewFrameTable creates a FrameTable with the given compression type.
func NewFrameTable(ct CompressionType) *FrameTable {
	return &FrameTable{compressionType: ct}
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

func (ft *FrameTable) Size() (uncompressed, compressed int64) {
	for _, frame := range ft.Frames {
		uncompressed += int64(frame.U)
		compressed += int64(frame.C)
	}

	return uncompressed, compressed
}

// Subset extracts frames overlapping range r, starting the scan at frame
// index `from`. It returns the index of the first included frame (not one
// past it) so that consecutive calls re-check the boundary frame — this is
// correct because a frame straddling two ranges must appear in both subsets.
func (ft *FrameTable) Subset(r Range, from int) (*FrameTable, int) {
	if ft == nil || r.Length == 0 {
		return nil, from
	}

	result := &FrameTable{
		compressionType: ft.compressionType,
	}

	// Advance currentOffset to frame `from`.
	currentOffset := ft.Offset
	for i := range from {
		if i >= len(ft.Frames) {
			break
		}
		currentOffset.Add(ft.Frames[i])
	}

	startSet := false
	requestedEnd := r.Offset + int64(r.Length)
	nextFrom := from

	for i := from; i < len(ft.Frames); i++ {
		frame := ft.Frames[i]
		frameEnd := currentOffset.U + int64(frame.U)

		if frameEnd <= r.Offset {
			currentOffset.Add(frame)
			nextFrom = i + 1

			continue
		}
		if currentOffset.U >= requestedEnd {
			break
		}

		if !startSet {
			result.Offset = currentOffset
			startSet = true
			nextFrom = i
		}
		result.Frames = append(result.Frames, frame)
		currentOffset.Add(frame)
	}

	if !startSet {
		return nil, nextFrom
	}

	return result, nextFrom
}

// locate finds the frame containing the given uncompressed offset and returns
// its start position (in both address spaces) and full size.
func (ft *FrameTable) locate(offset int64) (frameOffset FrameOffset, frameSize FrameSize, err error) {
	if ft == nil {
		return FrameOffset{}, FrameSize{}, fmt.Errorf("locate called with nil frame table - data is not compressed")
	}

	currentOffset := ft.Offset
	for _, frame := range ft.Frames {
		frameEnd := currentOffset.U + int64(frame.U)
		if offset >= currentOffset.U && offset < frameEnd {
			return currentOffset, frame, nil
		}
		currentOffset.Add(frame)
	}

	return FrameOffset{}, FrameSize{}, fmt.Errorf("offset %d is beyond the end of the frame table", offset)
}

// LocateCompressed returns the compressed (C-space) byte range for the frame
// containing the given uncompressed offset. Use this when you need to fetch
// raw compressed bytes from storage.
func (ft *FrameTable) LocateCompressed(offset int64) (Range, error) {
	start, size, err := ft.locate(offset)
	if err != nil {
		return Range{}, err
	}

	return Range{Offset: start.C, Length: int(size.C)}, nil
}

// LocateUncompressed returns the uncompressed (U-space) byte range for the
// frame containing the given uncompressed offset. Use this when you need to
// know the logical frame boundaries for cache alignment or chunk management.
func (ft *FrameTable) LocateUncompressed(offset int64) (Range, error) {
	start, size, err := ft.locate(offset)
	if err != nil {
		return Range{}, err
	}

	return Range{Offset: start.U, Length: int(size.U)}, nil
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
