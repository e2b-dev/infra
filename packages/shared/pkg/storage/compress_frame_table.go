package storage

import "fmt"

// Iterates over frames that overlap with the given range and calls fn for each frame.
func (ft *FrameTable) Range(start, length int64, fn func(offset FrameOffset, frame FrameSize) error) error {
	var currentOffset FrameOffset
	for _, frame := range ft.Frames {
		frameEnd := currentOffset.U + int64(frame.U)
		requestEnd := start + length
		if frameEnd <= start {
			// frame is before the requested range
			currentOffset.U += int64(frame.U)
			currentOffset.C += int64(frame.C)

			continue
		}
		if currentOffset.U >= requestEnd {
			// frame is after the requested range
			break
		}

		// frame overlaps with the requested range
		if err := fn(currentOffset, frame); err != nil {
			return err
		}
		currentOffset.U += int64(frame.U)
		currentOffset.C += int64(frame.C)
	}

	return nil
}

func (ft *FrameTable) TotalUncompressedSize() int64 {
	var total int64
	for _, frame := range ft.Frames {
		total += int64(frame.U)
	}

	return total
}

func (ft *FrameTable) TotalCompressedSize() int64 {
	var total int64
	for _, frame := range ft.Frames {
		total += int64(frame.C)
	}

	return total
}

// Subset returns a new FrameTable that represents the minimal set of frames
// that cover the start(length) range. Only entire frames are included (since
// they are compressed and can not be sliced). All offsets and sizes are in
// memory/uncompressed bytes. If the requested range extends beyond the total
// uncompressed size, the subset silently stops at the end of the frameset.
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
			// frame is before the requested range
			currentOffset.Add(frame)

			continue
		}
		if currentOffset.U >= requestedEnd {
			// frame is after the requested range
			break
		}

		if !startSet {
			newFrameTable.StartAt = currentOffset
			startSet = true
		}
		// frame overlaps with the requested range
		newFrameTable.Frames = append(newFrameTable.Frames, frame)
		currentOffset.Add(frame)
	}

	if !startSet {
		return nil, fmt.Errorf("requested range is beyond the end of the frame table")
	}

	return newFrameTable, nil
}

func (ft *FrameTable) FrameFor(r Range) (starts FrameOffset, size FrameSize, err error) {
	if ft == nil {
		// For nil frameTable (raw uncompressed data), return the requested range as-is.
		// U == C since there's no compression.
		return FrameOffset{U: r.Start, C: r.Start}, FrameSize{U: int32(r.Length), C: int32(r.Length)}, nil
	}
	subset, err := ft.Subset(r)
	if err != nil {
		return FrameOffset{}, FrameSize{}, err
	}

	if subset == nil || len(subset.Frames) == 0 {
		return FrameOffset{}, FrameSize{}, fmt.Errorf("no frames found for range %s", r)
	}
	if len(subset.Frames) > 1 {
		return FrameOffset{}, FrameSize{}, fmt.Errorf("range %s spans multiple frames", r)
	}

	return subset.StartAt, subset.Frames[0], nil
}

func (ft *FrameTable) GetFetchRange(rangeU Range) (Range, error) {
	fetchRange := rangeU
	if ft != nil && ft.CompressionType != CompressionNone {
		start, size, err := ft.FrameFor(rangeU)
		if err != nil {
			return Range{}, fmt.Errorf("getting frame for range %v: %w", rangeU, err)
		}
		fetchRange = Range{
			Start:  start.C,
			Length: int(size.C),
		}
	}

	return fetchRange, nil
}

// IsCompressed returns true if the frame table represents compressed data.
// Safe to call with nil - returns false.
func IsCompressed(ft *FrameTable) bool {
	return ft != nil && ft.CompressionType != CompressionNone
}

func (o *FrameOffset) Add(f FrameSize) {
	o.U += int64(f.U)
	o.C += int64(f.C)
}
