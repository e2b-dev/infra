package header

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/google/uuid"
)

type metadata struct {
	Version   int64
	BlockSize int64
	Size      int64
	Source    uuid.UUID
}

// Start, Length and SourceStart are in bytes of the data file
// Length will be a multiple of BlockSize
// The list of block mappings will be in order of increasing Start, covering the entire file
type blockMapping struct {
	Start       uint64
	Length      uint64
	Source      uuid.UUID
	SourceStart uint64
}

func (h *Header) Serialize(out io.Writer) error {
	err := binary.Write(out, binary.LittleEndian, h.metadata)
	if err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	for _, mapping := range h.blockMap {
		err := binary.Write(out, binary.LittleEndian, mapping)
		if err != nil {
			return fmt.Errorf("failed to write block mapping: %w", err)
		}
	}

	return nil
}

func Deserialize(in io.Reader) (*Header, error) {
	var metadata metadata

	err := binary.Read(in, binary.LittleEndian, &metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	mappings := make([]*blockMapping, 0)

	for {
		var m blockMapping
		err := binary.Read(in, binary.LittleEndian, &m)
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
