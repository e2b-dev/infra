package header

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type v3SerializableBuildMap struct {
	Offset             uint64
	Length             uint64
	BuildId            [16]byte // uuid.UUID
	BuildStorageOffset uint64
}

// serializeV3 writes [Metadata] [v3 mappings…] with no length prefix.
func serializeV3(metadata *Metadata, mappings []*BuildMap) ([]byte, error) {
	var buf bytes.Buffer

	if err := binary.Write(&buf, binary.LittleEndian, metadata); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	for _, mapping := range mappings {
		v3 := &v3SerializableBuildMap{
			Offset:             mapping.Offset,
			Length:             mapping.Length,
			BuildId:            mapping.BuildId,
			BuildStorageOffset: mapping.BuildStorageOffset,
		}
		if err := binary.Write(&buf, binary.LittleEndian, v3); err != nil {
			return nil, fmt.Errorf("failed to write block mapping: %w", err)
		}
	}

	return buf.Bytes(), nil
}

// deserializeV3 reads V3 mappings (read until EOF, no count prefix).
func deserializeV3(metadata *Metadata, blockData []byte) (*Header, error) {
	reader := bytes.NewReader(blockData)
	var mappings []*BuildMap

	for {
		var v3 v3SerializableBuildMap
		err := binary.Read(reader, binary.LittleEndian, &v3)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read block mapping: %w", err)
		}

		mappings = append(mappings, &BuildMap{
			Offset:             v3.Offset,
			Length:             v3.Length,
			BuildId:            v3.BuildId,
			BuildStorageOffset: v3.BuildStorageOffset,
		})
	}

	return NewHeader(metadata, mappings)
}
