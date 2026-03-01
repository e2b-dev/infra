package storage

import (
	"bytes"
	"fmt"
	"io"
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
	return fmt.Sprintf("U:%#x/C:%#x", o.U, o.C)
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
	return fmt.Sprintf("U:%#x/C:%#x", s.U, s.C)
}

type Range struct {
	Start  int64
	Length int
}

func (r Range) String() string {
	return fmt.Sprintf("%#x/%#x", r.Start, r.Length)
}

type FrameTable struct {
	CompressionType CompressionType
	StartAt         FrameOffset
	Frames          []FrameSize
}

// IsCompressed reports whether ft is non-nil and has a compression type set.
func IsCompressed(ft *FrameTable) bool {
	return ft != nil && ft.CompressionType != CompressionNone
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
// Stops silently at the end of the frameset if r extends beyond.
func (ft *FrameTable) Subset(r Range) (*FrameTable, error) {
	if ft == nil || r.Length == 0 {
		return nil, nil
	}
	if r.Start < ft.StartAt.U {
		return nil, fmt.Errorf("requested range starts before the beginning of the frame table")
	}
	newFrameTable := &FrameTable{
		CompressionType: ft.CompressionType,
	}

	startSet := false
	currentOffset := ft.StartAt
	requestedEnd := r.Start + int64(r.Length)
	for _, frame := range ft.Frames {
		frameEnd := currentOffset.U + int64(frame.U)
		if frameEnd <= r.Start {
			currentOffset.Add(frame)

			continue
		}
		if currentOffset.U >= requestedEnd {
			break
		}

		if !startSet {
			newFrameTable.StartAt = currentOffset
			startSet = true
		}
		newFrameTable.Frames = append(newFrameTable.Frames, frame)
		currentOffset.Add(frame)
	}

	if !startSet {
		return nil, fmt.Errorf("requested range is beyond the end of the frame table")
	}

	return newFrameTable, nil
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

	return FrameOffset{}, FrameSize{}, fmt.Errorf("offset %#x is beyond the end of the frame table", offset)
}

// GetFetchRange translates a U-space range to C-space using the frame table.
func (ft *FrameTable) GetFetchRange(rangeU Range) (Range, error) {
	fetchRange := rangeU
	if ft != nil && ft.CompressionType != CompressionNone {
		start, size, err := ft.FrameFor(rangeU.Start)
		if err != nil {
			return Range{}, fmt.Errorf("getting frame for offset %#x: %w", rangeU.Start, err)
		}
		endOffset := rangeU.Start + int64(rangeU.Length)
		frameEnd := start.U + int64(size.U)
		if endOffset > frameEnd {
			return Range{}, fmt.Errorf("range %v spans beyond frame ending at %#x", rangeU, frameEnd)
		}
		fetchRange = Range{
			Start:  start.C,
			Length: int(size.C),
		}
	}

	return fetchRange, nil
}

// DecompressReader decompresses from r into a new buffer of uncompressedSize.
func DecompressReader(ct CompressionType, r io.Reader, uncompressedSize int) ([]byte, error) {
	buf := make([]byte, uncompressedSize)

	switch ct {
	case CompressionZstd:
		dec, err := getZstdDecoder(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}
		defer putZstdDecoder(dec)

		n, err := io.ReadFull(dec, buf)
		if err != nil {
			return nil, fmt.Errorf("zstd decompress: %w", err)
		}

		return buf[:n], nil

	case CompressionLZ4:
		rd := getLZ4Reader(r)
		defer putLZ4Reader(rd)

		n, err := io.ReadFull(rd, buf)
		if err != nil {
			return nil, fmt.Errorf("lz4 decompress: %w", err)
		}

		return buf[:n], nil

	default:
		return nil, fmt.Errorf("unsupported compression type: %d", ct)
	}
}

// DecompressFrame decompresses an in-memory compressed byte slice.
func DecompressFrame(ct CompressionType, compressed []byte, uncompressedSize int32) ([]byte, error) {
	return DecompressReader(ct, bytes.NewReader(compressed), int(uncompressedSize))
}
