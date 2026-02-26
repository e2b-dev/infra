package header

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/compress"
)

const metadataVersion = 4

type Metadata struct {
	Version     uint64
	BlockSize   uint64
	Size        uint64
	Generation  uint64
	BuildId     uuid.UUID
	BaseBuildId uuid.UUID
}

func NewTemplateMetadata(buildId uuid.UUID, blockSize, size uint64) *Metadata {
	return &Metadata{
		Version:     metadataVersion,
		Generation:  0,
		BlockSize:   blockSize,
		Size:        size,
		BuildId:     buildId,
		BaseBuildId: buildId,
	}
}

func (m *Metadata) NextGeneration(buildID uuid.UUID) *Metadata {
	return &Metadata{
		Version:     metadataVersion,
		Generation:  m.Generation + 1,
		BlockSize:   m.BlockSize,
		Size:        m.Size,
		BuildId:     buildID,
		BaseBuildId: m.BaseBuildId,
	}
}

// Serialize format:
//
//	[Metadata]
//	[BuildMap entries...]
//	--- v4+ only ---
//	[numFrameTables uint32]
//	for each frame table:
//	  [BuildID uuid]
//	  [numFrames uint32]
//	  [FrameEntry * numFrames]
func Serialize(metadata *Metadata, mappings []*BuildMap) ([]byte, error) {
	return SerializeWithFrames(metadata, mappings, nil)
}

func SerializeWithFrames(metadata *Metadata, mappings []*BuildMap, frameTables map[uuid.UUID]*FrameTable) ([]byte, error) {
	var buf bytes.Buffer

	if err := binary.Write(&buf, binary.LittleEndian, metadata); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	for _, mapping := range mappings {
		if err := binary.Write(&buf, binary.LittleEndian, mapping); err != nil {
			return nil, fmt.Errorf("failed to write block mapping: %w", err)
		}
	}

	if metadata.Version >= 4 {
		// Frame table section:
		//   frameSize   uint32
		//   numTables   uint32
		//   per table:
		//     buildID          [16]byte
		//     uncompressedSize uint64
		//     numFrames        uint32
		//     entries[]         (index uint32, compOffset uint64, compSize uint32) × numFrames
		if err := binary.Write(&buf, binary.LittleEndian, uint32(compress.FrameSize)); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(frameTables))); err != nil {
			return nil, err
		}
		for _, ft := range frameTables {
			if err := binary.Write(&buf, binary.LittleEndian, ft.BuildID); err != nil {
				return nil, err
			}
			if err := binary.Write(&buf, binary.LittleEndian, ft.UncompressedSize); err != nil {
				return nil, err
			}
			if err := binary.Write(&buf, binary.LittleEndian, uint32(len(ft.Frames))); err != nil {
				return nil, err
			}
			for _, f := range ft.Frames {
				if err := binary.Write(&buf, binary.LittleEndian, f.Index); err != nil {
					return nil, err
				}
				if err := binary.Write(&buf, binary.LittleEndian, f.CompressedOffset); err != nil {
					return nil, err
				}
				if err := binary.Write(&buf, binary.LittleEndian, f.CompressedSize); err != nil {
					return nil, err
				}
			}
		}
	}

	return buf.Bytes(), nil
}

func Deserialize(ctx context.Context, in storage.Blob) (*Header, error) {
	data, err := storage.GetBlob(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("failed to write to buffer: %w", err)
	}

	return DeserializeBytes(data)
}

func DeserializeBytes(data []byte) (*Header, error) {
	reader := bytes.NewReader(data)
	var metadata Metadata
	if err := binary.Read(reader, binary.LittleEndian, &metadata); err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	// Read mappings. For v3 and below, mappings go until EOF.
	// For v4+, we need to detect when mappings end and frame tables begin.
	// Since mappings have a fixed size, we read BuildMap-sized chunks and
	// check if the remaining bytes can hold a frame table section.
	mappings := make([]*BuildMap, 0)

	if metadata.Version < 4 {
		// Legacy: read until EOF.
		for {
			var m BuildMap
			err := binary.Read(reader, binary.LittleEndian, &m)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read block mapping: %w", err)
			}
			mappings = append(mappings, &m)
		}

		return NewHeader(&metadata, mappings)
	}

	// V4+: mappings followed by frame tables.
	// We need to figure out where mappings end. The frame table section starts
	// with a uint32 count. Since BuildMap is 48 bytes and we know the total
	// remaining bytes, we can compute: remaining = nMappings*48 + frameTableBytes.
	// But we don't know nMappings upfront.
	//
	// Strategy: try reading BuildMap entries. After each one, peek at whether
	// the remaining data could be a valid frame table section. The frame table
	// section starts with a uint32 that is a plausible count (< 10000), followed
	// by uuid+uint32 pairs.
	//
	// Simpler: just read mappings until the remaining data is too small for another
	// BuildMap (48 bytes) or we detect the frame table marker.
	buildMapSize := int64(binary.Size(BuildMap{}))
	for {
		remaining := int64(reader.Len())
		if remaining < buildMapSize {
			// Not enough for another mapping — rest must be frame tables.
			break
		}

		// Save position to rewind if this isn't a mapping.
		pos, _ := reader.Seek(0, io.SeekCurrent)

		var m BuildMap
		if err := binary.Read(reader, binary.LittleEndian, &m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to read block mapping: %w", err)
		}

		// Heuristic: if we've consumed all mappings and the next bytes are the
		// frame table count, the BuildMap we just read would have nonsensical
		// values. Check: valid mappings have Offset < metadata.Size.
		if m.Offset >= metadata.Size && metadata.Size > 0 {
			// This wasn't a mapping — rewind and break.
			reader.Seek(pos, io.SeekStart)
			break
		}

		mappings = append(mappings, &m)
	}

	// Read frame tables.
	var frameTables map[uuid.UUID]*FrameTable

	if reader.Len() >= 8 {
		var frameSize uint32
		if err := binary.Read(reader, binary.LittleEndian, &frameSize); err != nil {
			return nil, fmt.Errorf("failed to read frame size: %w", err)
		}

		var numTables uint32
		if err := binary.Read(reader, binary.LittleEndian, &numTables); err == nil && numTables > 0 {
			frameTables = make(map[uuid.UUID]*FrameTable, numTables)

			for range numTables {
				var buildID uuid.UUID
				if err := binary.Read(reader, binary.LittleEndian, &buildID); err != nil {
					return nil, fmt.Errorf("failed to read frame table build ID: %w", err)
				}
				var totalUncomp uint64
				if err := binary.Read(reader, binary.LittleEndian, &totalUncomp); err != nil {
					return nil, fmt.Errorf("failed to read uncompressed size: %w", err)
				}
				var nFrames uint32
				if err := binary.Read(reader, binary.LittleEndian, &nFrames); err != nil {
					return nil, fmt.Errorf("failed to read frame count: %w", err)
				}

				frames := make([]FrameEntry, nFrames)
				for j := range frames {
					if err := binary.Read(reader, binary.LittleEndian, &frames[j].Index); err != nil {
						return nil, fmt.Errorf("failed to read frame %d index: %w", j, err)
					}
					if err := binary.Read(reader, binary.LittleEndian, &frames[j].CompressedOffset); err != nil {
						return nil, fmt.Errorf("failed to read frame %d offset: %w", j, err)
					}
					if err := binary.Read(reader, binary.LittleEndian, &frames[j].CompressedSize); err != nil {
						return nil, fmt.Errorf("failed to read frame %d size: %w", j, err)
					}
				}
				frameTables[buildID] = &FrameTable{BuildID: buildID, UncompressedSize: totalUncomp, Frames: frames}
			}
		}
	}

	h, err := NewHeader(&metadata, mappings)
	if err != nil {
		return nil, err
	}
	h.FrameTables = frameTables

	return h, nil
}
