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

type FrameSize struct {
	U int32
	C int32
}

func (s FrameSize) String() string {
	return fmt.Sprintf("U:%d/C:%d", s.U, s.C)
}

type Range struct {
	Start  int64
	Length int
}

func (r Range) String() string {
	return fmt.Sprintf("%d/%d", r.Start, r.Length)
}

type FrameTable struct {
	compressionType CompressionType
	StartAt         FrameOffset
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

// Range calls fn for each frame overlapping [start, start+length).
func (ft *FrameTable) Range(start, length int64, fn func(offset FrameOffset, frame FrameSize) error) error {
	currentOffset := ft.StartAt
	for _, frame := range ft.Frames {
		frameEnd := currentOffset.U + int64(frame.U)
		requestEnd := start + length
		if frameEnd <= start {
			currentOffset.U += int64(frame.U)
			currentOffset.C += int64(frame.C)

			continue
		}
		if currentOffset.U >= requestEnd {
			break
		}

		if err := fn(currentOffset, frame); err != nil {
			return err
		}
		currentOffset.U += int64(frame.U)
		currentOffset.C += int64(frame.C)
	}

	return nil
}

func (ft *FrameTable) Size() (uncompressed, compressed int64) {
	for _, frame := range ft.Frames {
		uncompressed += int64(frame.U)
		compressed += int64(frame.C)
	}

	return uncompressed, compressed
}

// Subset returns frames covering r. Whole frames only (can't split compressed).
func (ft *FrameTable) Subset(r Range) (*FrameTable, error) {
	if ft == nil || r.Length == 0 {
		return nil, nil
	}
	if r.Start < ft.StartAt.U {
		return nil, fmt.Errorf("requested range starts before the beginning of the frame table")
	}

	result, _ := ft.SubsetFrom(r, 0)
	if result == nil {
		return nil, fmt.Errorf("requested range is beyond the end of the frame table")
	}

	return result, nil
}

// SubsetFrom is like Subset but starts scanning from frame index `from`,
// returning the index of the first frame past the result. Use this to
// efficiently extract consecutive subsets from a sorted sequence of ranges
// without re-scanning from the beginning each time.
func (ft *FrameTable) SubsetFrom(r Range, from int) (*FrameTable, int) {
	if ft == nil || r.Length == 0 {
		return nil, from
	}

	result := &FrameTable{
		compressionType: ft.compressionType,
	}

	// Advance currentOffset to frame `from`.
	currentOffset := ft.StartAt
	for i := range from {
		if i >= len(ft.Frames) {
			break
		}
		currentOffset.Add(ft.Frames[i])
	}

	startSet := false
	requestedEnd := r.Start + int64(r.Length)
	nextFrom := from

	for i := from; i < len(ft.Frames); i++ {
		frame := ft.Frames[i]
		frameEnd := currentOffset.U + int64(frame.U)

		if frameEnd <= r.Start {
			currentOffset.Add(frame)
			nextFrom = i + 1

			continue
		}
		if currentOffset.U >= requestedEnd {
			break
		}

		if !startSet {
			result.StartAt = currentOffset
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

// FrameFor finds the frame containing the given offset and returns its start position and full size.
func (ft *FrameTable) FrameFor(offset int64) (starts FrameOffset, size FrameSize, err error) {
	if ft == nil {
		return FrameOffset{}, FrameSize{}, fmt.Errorf("FrameFor called with nil frame table - data is not compressed")
	}

	currentOffset := ft.StartAt
	for _, frame := range ft.Frames {
		frameEnd := currentOffset.U + int64(frame.U)
		if offset >= currentOffset.U && offset < frameEnd {
			return currentOffset, frame, nil
		}
		currentOffset.Add(frame)
	}

	return FrameOffset{}, FrameSize{}, fmt.Errorf("offset %d is beyond the end of the frame table", offset)
}

// GetFetchRange translates a U-space range to C-space using the frame table.
func (ft *FrameTable) GetFetchRange(rangeU Range) (Range, error) {
	fetchRange := rangeU
	if ft.IsCompressed() {
		start, size, err := ft.FrameFor(rangeU.Start)
		if err != nil {
			return Range{}, fmt.Errorf("getting frame for offset %d: %w", rangeU.Start, err)
		}
		endOffset := rangeU.Start + int64(rangeU.Length)
		frameEnd := start.U + int64(size.U)
		if endOffset > frameEnd {
			return Range{}, fmt.Errorf("range %v spans beyond frame ending at %d", rangeU, frameEnd)
		}
		fetchRange = Range{
			Start:  start.C,
			Length: int(size.C),
		}
	}

	return fetchRange, nil
}
