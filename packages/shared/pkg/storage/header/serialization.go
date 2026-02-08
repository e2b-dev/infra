package header

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const metadataVersion = 4

type Metadata struct {
	Version    uint64
	BlockSize  uint64
	Size       uint64
	Generation uint64
	BuildId    uuid.UUID
	// TODO: Use the base build id when setting up the snapshot rootfs
	BaseBuildId uuid.UUID
}

type v3SerializableBuildMap struct {
	Offset             uint64
	Length             uint64
	BuildId            uuid.UUID
	BuildStorageOffset uint64
}
type v4SerializableBuildMap struct {
	Offset                   uint64
	Length                   uint64
	BuildId                  uuid.UUID
	BuildStorageOffset       uint64
	CompressionTypeNumFrames uint64 // CompressionType is stored as uint8 in the high byte, the low 24 bits are NumFrames

	// if CompressionType != CompressionNone and there are frames
	// - followed by frames offset (16 bytes)
	// - followed by frames... (16 bytes * NumFrames)
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

func Serialize(metadata *Metadata, mappings []*BuildMap) ([]byte, error) {
	var buf bytes.Buffer

	err := binary.Write(&buf, binary.LittleEndian, metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	var v any
	for _, mapping := range mappings {
		var offset *storage.FrameOffset
		var frames []storage.FrameSize
		if metadata.Version <= 3 {
			v = &v3SerializableBuildMap{
				Offset:             mapping.Offset,
				Length:             mapping.Length,
				BuildId:            mapping.BuildId,
				BuildStorageOffset: mapping.BuildStorageOffset,
			}
		} else {
			v4 := &v4SerializableBuildMap{
				Offset:             mapping.Offset,
				Length:             mapping.Length,
				BuildId:            mapping.BuildId,
				BuildStorageOffset: mapping.BuildStorageOffset,
			}
			if mapping.FrameTable != nil {
				v4.CompressionTypeNumFrames = uint64(mapping.FrameTable.CompressionType)<<24 | uint64(len(mapping.FrameTable.Frames))
				offset = &mapping.FrameTable.StartAt
				frames = mapping.FrameTable.Frames
			}
			v = v4
		}

		err := binary.Write(&buf, binary.LittleEndian, v)
		if err != nil {
			return nil, fmt.Errorf("failed to write block mapping: %w", err)
		}
		if offset != nil {
			err := binary.Write(&buf, binary.LittleEndian, offset)
			if err != nil {
				return nil, fmt.Errorf("failed to write compression frames starting offset: %w", err)
			}
		}
		for _, frame := range frames {
			err := binary.Write(&buf, binary.LittleEndian, frame)
			if err != nil {
				return nil, fmt.Errorf("failed to write compression frame: %w", err)
			}
		}
	}

	return buf.Bytes(), nil
}

func Deserialize(data []byte) (*Header, error) {
	var metadata Metadata
	in := bytes.NewReader(data)
	err := binary.Read(in, binary.LittleEndian, &metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	mappings := make([]*BuildMap, 0)

MAPPINGS:
	for {
		var m BuildMap

		switch metadata.Version {
		case 0, 1, 2, 3:
			var v3 v3SerializableBuildMap
			err = binary.Read(in, binary.LittleEndian, &v3)
			if errors.Is(err, io.EOF) {
				break MAPPINGS
			}

			m.Offset = v3.Offset
			m.Length = v3.Length
			m.BuildId = v3.BuildId
			m.BuildStorageOffset = v3.BuildStorageOffset

		case 4:
			var v4 v4SerializableBuildMap
			err = binary.Read(in, binary.LittleEndian, &v4)
			if errors.Is(err, io.EOF) {
				break MAPPINGS
			}

			m.Offset = v4.Offset
			m.Length = v4.Length
			m.BuildId = v4.BuildId
			m.BuildStorageOffset = v4.BuildStorageOffset
			if v4.CompressionTypeNumFrames != 0 {
				m.FrameTable = &storage.FrameTable{
					CompressionType: storage.CompressionType((v4.CompressionTypeNumFrames >> 24) & 0xFF),
				}
				numFrames := v4.CompressionTypeNumFrames & 0xFFFFFF

				var startAt storage.FrameOffset
				err = binary.Read(in, binary.LittleEndian, &startAt)
				if err != nil {
					return nil, fmt.Errorf("failed to read compression frames starting offset: %w", err)
				}
				m.FrameTable.StartAt = startAt

				for range numFrames {
					var frame storage.FrameSize
					err = binary.Read(in, binary.LittleEndian, &frame)
					if err != nil {
						return nil, fmt.Errorf("failed to read the expected compression frame: %w", err)
					}
					m.FrameTable.Frames = append(m.FrameTable.Frames, frame)
				}
			}
		}

		mappings = append(mappings, &m)
	}

	header, err := NewHeader(&metadata, mappings)
	if err != nil {
		return nil, err
	}

	// Validate header integrity after deserialization
	if err := ValidateHeader(header); err != nil {
		return nil, fmt.Errorf("header validation failed: %w", err)
	}

	return header, nil
}
