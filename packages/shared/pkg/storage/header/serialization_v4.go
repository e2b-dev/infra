package header

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"io"
	"slices"

	"github.com/google/uuid"
	lz4 "github.com/pierrec/lz4/v4"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type v4SerializableBuildMap struct {
	Offset             uint64
	Length             uint64
	BuildId            [16]byte // uuid.UUID
	BuildStorageOffset uint64
	CompressionType    uint32
	NumFrames          uint32

	// if CompressionType != CompressionNone and NumFrames > 0:
	// - followed by FrameOffset (16 bytes)
	// - followed by FrameSize × NumFrames (8 bytes each)
}

// v4SerializableBuildFileInfo is the on-disk format for a BuildFileInfo entry.
type v4SerializableBuildFileInfo struct {
	BuildId  uuid.UUID
	Size     int64
	Checksum [32]byte
}

// serializeV4 writes [Metadata] [uint32 uncompressedSize] [LZ4( BuildFiles + counted mappings + FrameTables )].
func serializeV4(metadata *Metadata, buildFiles map[uuid.UUID]BuildFileInfo, mappings []*BuildMap) ([]byte, error) {
	// --- raw metadata prefix (not compressed) ---
	var metaBuf bytes.Buffer
	if err := binary.Write(&metaBuf, binary.LittleEndian, metadata); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	// --- compressed block: build-info + mappings + frame tables ---
	var block bytes.Buffer

	// Build-info section.
	if err := binary.Write(&block, binary.LittleEndian, uint32(len(buildFiles))); err != nil {
		return nil, fmt.Errorf("failed to write build files count: %w", err)
	}

	// Sort by UUID for deterministic serialization.
	buildIDs := make([]uuid.UUID, 0, len(buildFiles))
	for id := range buildFiles {
		buildIDs = append(buildIDs, id)
	}
	slices.SortFunc(buildIDs, func(a, b uuid.UUID) int {
		return cmp.Compare(a.String(), b.String())
	})

	for _, id := range buildIDs {
		info := buildFiles[id]
		entry := v4SerializableBuildFileInfo{
			BuildId:  id,
			Size:     info.Size,
			Checksum: info.Checksum,
		}
		if err := binary.Write(&block, binary.LittleEndian, &entry); err != nil {
			return nil, fmt.Errorf("failed to write build file info: %w", err)
		}
	}

	// Counted mappings with inline FrameTables.
	if err := binary.Write(&block, binary.LittleEndian, uint32(len(mappings))); err != nil {
		return nil, fmt.Errorf("failed to write mappings count: %w", err)
	}

	for _, mapping := range mappings {
		v4 := &v4SerializableBuildMap{
			Offset:             mapping.Offset,
			Length:             mapping.Length,
			BuildId:            mapping.BuildId,
			BuildStorageOffset: mapping.BuildStorageOffset,
		}

		var offset *storage.FrameOffset
		var frames []storage.FrameSize
		if mapping.FrameTable != nil {
			v4.CompressionType = uint32(mapping.FrameTable.CompressionType())
			v4.NumFrames = uint32(len(mapping.FrameTable.Frames))
			if v4.CompressionType != 0 && v4.NumFrames > 0 {
				offset = &mapping.FrameTable.StartAt
				frames = mapping.FrameTable.Frames
			}
		}

		if err := binary.Write(&block, binary.LittleEndian, v4); err != nil {
			return nil, fmt.Errorf("failed to write block mapping: %w", err)
		}
		if offset != nil {
			if err := binary.Write(&block, binary.LittleEndian, offset); err != nil {
				return nil, fmt.Errorf("failed to write compression frames starting offset: %w", err)
			}
		}
		for _, frame := range frames {
			if err := binary.Write(&block, binary.LittleEndian, frame); err != nil {
				return nil, fmt.Errorf("failed to write compression frame: %w", err)
			}
		}
	}

	// LZ4-compress the block and assemble: [metadata] [uint32 size] [compressed block].
	blockBytes := block.Bytes()
	compressed, err := compressLZ4(blockBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-compress v4 header block: %w", err)
	}

	result := make([]byte, metadataSize+4+len(compressed))
	copy(result, metaBuf.Bytes())
	binary.LittleEndian.PutUint32(result[metadataSize:], uint32(len(blockBytes)))
	copy(result[metadataSize+4:], compressed)

	return result, nil
}

// deserializeV4 decompresses and reads the V4 block: build-info + counted mappings + FrameTables.
func deserializeV4(metadata *Metadata, blockData []byte) (*Header, error) {
	if len(blockData) < 4 {
		return nil, fmt.Errorf("v4 header block too short for size prefix: %d bytes", len(blockData))
	}

	decompressed, err := decompressLZ4(blockData[4:])
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-decompress v4 header block: %w", err)
	}

	reader := bytes.NewReader(decompressed)

	// Build-info section.
	var numBuilds uint32
	if err := binary.Read(reader, binary.LittleEndian, &numBuilds); err != nil {
		return nil, fmt.Errorf("failed to read build files count: %w", err)
	}

	var buildFiles map[uuid.UUID]BuildFileInfo
	if numBuilds > 0 {
		buildFiles = make(map[uuid.UUID]BuildFileInfo, numBuilds)
		for range numBuilds {
			var entry v4SerializableBuildFileInfo
			if err := binary.Read(reader, binary.LittleEndian, &entry); err != nil {
				return nil, fmt.Errorf("failed to read build file info: %w", err)
			}
			buildFiles[entry.BuildId] = BuildFileInfo{
				Size:     entry.Size,
				Checksum: entry.Checksum,
			}
		}
	}

	// Counted mappings with inline FrameTables.
	var numMappings uint32
	if err := binary.Read(reader, binary.LittleEndian, &numMappings); err != nil {
		return nil, fmt.Errorf("failed to read mappings count: %w", err)
	}

	mappings := make([]*BuildMap, 0, numMappings)
	for range numMappings {
		var v4 v4SerializableBuildMap
		if err := binary.Read(reader, binary.LittleEndian, &v4); err != nil {
			return nil, fmt.Errorf("failed to read block mapping: %w", err)
		}

		m := &BuildMap{
			Offset:             v4.Offset,
			Length:             v4.Length,
			BuildId:            v4.BuildId,
			BuildStorageOffset: v4.BuildStorageOffset,
		}

		if v4.CompressionType != 0 && v4.NumFrames > 0 {
			m.FrameTable = storage.NewFrameTable(storage.CompressionType(v4.CompressionType))
			numFrames := v4.NumFrames

			var startAt storage.FrameOffset
			if err := binary.Read(reader, binary.LittleEndian, &startAt); err != nil {
				return nil, fmt.Errorf("failed to read compression frames starting offset: %w", err)
			}
			m.FrameTable.StartAt = startAt

			for range numFrames {
				var frame storage.FrameSize
				if err := binary.Read(reader, binary.LittleEndian, &frame); err != nil {
					return nil, fmt.Errorf("failed to read the expected compression frame: %w", err)
				}
				m.FrameTable.Frames = append(m.FrameTable.Frames, frame)
			}
		}

		mappings = append(mappings, m)
	}

	h, err := NewHeader(metadata, mappings)
	if err != nil {
		return nil, err
	}
	h.BuildFiles = buildFiles

	return h, nil
}

// compressLZ4 compresses data for V4 header serialization using the LZ4
// streaming API. Settings are fixed for the V4 wire format.
func compressLZ4(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(data))

	w := lz4.NewWriter(&buf)
	w.Apply(
		lz4.BlockSizeOption(lz4.Block4Mb),
		lz4.BlockChecksumOption(true),
		lz4.ChecksumOption(true),
		lz4.CompressionLevelOption(lz4.Fast),
	)

	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("lz4 compress: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("lz4 compress close: %w", err)
	}

	return buf.Bytes(), nil
}

// decompressLZ4 decompresses an LZ4 frame from V4 header data.
func decompressLZ4(src []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(src))

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}

	return data, nil
}
