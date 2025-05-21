package header

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/google/uuid"
)

type Metadata struct {
	Version    uint64
	BlockSize  uint64
	Size       uint64
	Generation uint64
	BuildId    uuid.UUID
	// TODO: Use the base build id when setting up the snapshot rootfs
	BaseBuildId uuid.UUID
}

func NewTemplateMetadata(buildId uuid.UUID, blockSize, size uint64) *Metadata {
	return &Metadata{
		Version:     1,
		Generation:  0,
		BlockSize:   blockSize,
		Size:        size,
		BuildId:     buildId,
		BaseBuildId: buildId,
	}
}

func (m *Metadata) NextGeneration(buildID uuid.UUID) *Metadata {
	return &Metadata{
		Version:     1,
		Generation:  m.Generation + 1,
		BlockSize:   m.BlockSize,
		Size:        m.Size,
		BuildId:     buildID,
		BaseBuildId: m.BaseBuildId,
	}
}

func Serialize(metadata *Metadata, mappings []*BuildMap) (io.Reader, error) {
	var buf bytes.Buffer

	err := binary.Write(&buf, binary.LittleEndian, metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	for _, mapping := range mappings {
		err := binary.Write(&buf, binary.LittleEndian, mapping)
		if err != nil {
			return nil, fmt.Errorf("failed to write block mapping: %w", err)
		}
	}

	return &buf, nil
}

func Deserialize(in io.WriterTo) (*Header, error) {
	var buf bytes.Buffer

	_, err := in.WriteTo(&buf)
	if err != nil {
		return nil, fmt.Errorf("failed to write to buffer: %w", err)
	}

	reader := bytes.NewReader(buf.Bytes())

	var metadata Metadata

	err = binary.Read(reader, binary.LittleEndian, &metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	mappings := make([]*BuildMap, 0)

	for {
		var m BuildMap
		err := binary.Read(reader, binary.LittleEndian, &m)
		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("failed to read block mapping: %w", err)
		}

		mappings = append(mappings, &m)
	}

	return NewHeader(&metadata, mappings), nil
}
