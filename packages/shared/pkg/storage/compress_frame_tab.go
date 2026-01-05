package storage

// Iterates over frames that overlap with the given range and calls fn for each frame.
func (ci *FrameTable) Range(start, length int64, fn func(offset Offset, frame Frame) error) error {
	var currentOffset Offset
	for _, frame := range ci.Frames {
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

func (ci *FrameTable) TotalUncompressedSize() int64 {
	var total int64
	for _, frame := range ci.Frames {
		total += int64(frame.U)
	}

	return total
}

func (ci *FrameTable) TotalCompressedSize() int64 {
	var total int64
	for _, frame := range ci.Frames {
		total += int64(frame.C)
	}

	return total
}

// Subset returns a new CompressedInfo that represents the minimal set of frames
// that cover the start(length) range. Only entire frames are included (since
// they are compressed and can not be sliced). All offsets and sizes are in
// memory/uncompressed bytes. If the requested range extends beyond the total
// uncompressed size, the subset silently stops at the end of the frameset.
func (ci *FrameTable) Subset(start int64, length int64) *FrameTable {
	if ci == nil {
		return nil
	}
	newCI := &FrameTable{
		CompressionType: ci.CompressionType,
	}

	var currentOffset int64
	requestedEnd := start + length
	for _, frame := range ci.Frames {
		frameEnd := currentOffset + int64(frame.U)
		if frameEnd <= start {
			// frame is before the requested range
			currentOffset += int64(frame.U)

			continue
		}
		if currentOffset >= requestedEnd {
			// frame is after the requested range
			break
		}

		// frame overlaps with the requested range
		newCI.Frames = append(newCI.Frames, frame)
		currentOffset += int64(frame.U)
	}

	return newCI
}
