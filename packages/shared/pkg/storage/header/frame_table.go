package header

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/compress"
)

// PruneToMappings returns a new FrameTable containing only frames referenced
// by the given mappings for this build.
func (ft *FrameTable) PruneToMappings(mappings []*BuildMap) *FrameTable {
	if ft == nil {
		return nil
	}

	needed := make(map[uint32]bool)
	for _, m := range mappings {
		if m.BuildId != ft.BuildID {
			continue
		}
		startFrame := uint32(m.BuildStorageOffset / uint64(compress.FrameSize))
		endByte := m.BuildStorageOffset + m.Length
		endFrame := uint32((endByte + uint64(compress.FrameSize) - 1) / uint64(compress.FrameSize))
		for f := startFrame; f < endFrame; f++ {
			needed[f] = true
		}
	}

	pruned := make([]FrameEntry, 0, len(needed))
	for _, f := range ft.Frames {
		if needed[f.Index] {
			pruned = append(pruned, f)
		}
	}

	return &FrameTable{
		BuildID:          ft.BuildID,
		UncompressedSize: ft.UncompressedSize,
		Frames:           pruned,
	}
}

// FrameEntry describes one compressed frame.
type FrameEntry struct {
	Index            uint32 // frame index = uncompressed_offset / FrameSize
	CompressedOffset uint64
	CompressedSize   uint32
}

// FrameTable holds compressed frame info for a single build's data file.
type FrameTable struct {
	BuildID          uuid.UUID
	UncompressedSize uint64       // total uncompressed size of the build's data file
	Frames           []FrameEntry // sorted by Index
}

// NewFrameTable creates a frame table from compress output.
func NewFrameTable(buildID uuid.UUID, frames []compress.FrameInfo) *FrameTable {
	entries := make([]FrameEntry, len(frames))
	var totalUncomp uint64
	for i, f := range frames {
		entries[i] = FrameEntry{
			Index:            uint32(i),
			CompressedOffset: f.CompressedOffset,
			CompressedSize:   uint32(f.CompressedSize),
		}
		totalUncomp += uint64(f.UncompressedSize)
	}
	return &FrameTable{BuildID: buildID, UncompressedSize: totalUncomp, Frames: entries}
}

// ToFrameInfo converts to compress.FrameInfo for the reader.
func (ft *FrameTable) ToFrameInfo() []compress.FrameInfo {
	if ft == nil || len(ft.Frames) == 0 {
		return nil
	}

	totalFrames := (ft.UncompressedSize + uint64(compress.FrameSize) - 1) / uint64(compress.FrameSize)
	lastIdx := uint32(totalFrames - 1)
	lastFrameSize := uint32(ft.UncompressedSize - uint64(lastIdx)*uint64(compress.FrameSize))

	out := make([]compress.FrameInfo, len(ft.Frames))
	for i, f := range ft.Frames {
		uncompSz := uint32(compress.FrameSize)
		if f.Index == lastIdx {
			uncompSz = lastFrameSize
		}
		out[i] = compress.FrameInfo{
			Index:            f.Index,
			CompressedOffset: f.CompressedOffset,
			CompressedSize:   f.CompressedSize,
			UncompressedSize: uncompSz,
		}
	}
	return out
}
