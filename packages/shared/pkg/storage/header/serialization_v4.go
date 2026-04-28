// V4 header binary format:
//
//	[ Metadata                      ] // fixed-size, binary.LittleEndian
//	[ uint32 uncompressedBlockSize  ] // little-endian, size of the inner block
//	[ LZ4(block)                    ] // block layout below
//
// Inner block (LZ4-compressed), all little-endian:
//
//	[ v4SerializableDependency      ] // self: BuildId, FileSize, Checksum
//	[ FrameTable                    ] // self frame table (trimmed)
//	[ uint32  numParents            ]
//	[ numParents × (
//	    v4SerializableDependency
//	    FrameTable (trimmed)
//	  )                             ]
//	[ uint32  numMappings           ]
//	[ numMappings × v4SerializableBuildMap{Offset, Length, BuildId, BuildStorageOffset} ]
package header

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"slices"

	"github.com/google/uuid"
	lz4 "github.com/pierrec/lz4/v4"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// MetadataVersionV4 is used for compressed builds (V4 headers with FrameTables).
const MetadataVersionV4 = 4

const v4SizePrefixLen = 4

type v4SerializableBuildMap struct {
	Offset             uint64
	Length             uint64
	BuildId            [16]byte // uuid.UUID
	BuildStorageOffset uint64
}

type v4SerializableDependency struct {
	BuildId  uuid.UUID
	FileSize int64
	Checksum [32]byte
}

// SerializeV4 — callers must Finalize self first; an unresolved self errors
// out rather than emitting a wire blob without proper self metadata.
func (t *Header) SerializeV4() ([]byte, error) {
	var toCompress bytes.Buffer
	var metaBuf bytes.Buffer

	meta := *t.Metadata
	meta.Version = MetadataVersionV4

	if err := binary.Write(&metaBuf, binary.LittleEndian, &meta); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	self, err := t.selfDependency()
	if err != nil {
		return nil, fmt.Errorf("serialize v4: self dep: %w", err)
	}

	perBuildRanges := extractRelevantRanges(t.Mapping)

	self.FrameTable = self.FrameTable.TrimToRanges(perBuildRanges[t.Metadata.BuildId])
	if err := serializeDependency(&toCompress, t.Metadata.BuildId, self); err != nil {
		return nil, fmt.Errorf("write self dep: %w", err)
	}

	parentDependencies := t.parentDependencies()
	parentIDs := make([]uuid.UUID, 0, len(parentDependencies))
	for id := range parentDependencies {
		parentIDs = append(parentIDs, id)
	}
	slices.SortFunc(parentIDs, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})

	if err := binary.Write(&toCompress, binary.LittleEndian, uint32(len(parentIDs))); err != nil {
		return nil, fmt.Errorf("failed to write parent count: %w", err)
	}
	for _, id := range parentIDs {
		dep := parentDependencies[id]
		dep.FrameTable = dep.FrameTable.TrimToRanges(perBuildRanges[id])
		if err := serializeDependency(&toCompress, id, dep); err != nil {
			return nil, fmt.Errorf("write parent dep %s: %w", id, err)
		}
	}

	if err := binary.Write(&toCompress, binary.LittleEndian, uint32(len(t.Mapping))); err != nil {
		return nil, fmt.Errorf("failed to write mappings count: %w", err)
	}

	for _, mapping := range t.Mapping {
		v4 := &v4SerializableBuildMap{
			Offset:             mapping.Offset,
			Length:             mapping.Length,
			BuildId:            mapping.BuildId,
			BuildStorageOffset: mapping.BuildStorageOffset,
		}

		if err := binary.Write(&toCompress, binary.LittleEndian, v4); err != nil {
			return nil, fmt.Errorf("failed to write block mapping: %w", err)
		}
	}

	blockBytes := toCompress.Bytes()
	compressed, err := compressLZ4(blockBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-compress v4 header block: %w", err)
	}

	result := make([]byte, metadataSize+v4SizePrefixLen+len(compressed))
	copy(result, metaBuf.Bytes())
	binary.LittleEndian.PutUint32(result[metadataSize:], uint32(len(blockBytes)))
	copy(result[metadataSize+v4SizePrefixLen:], compressed)

	return result, nil
}

func deserializeV4(metadata *Metadata, blockData []byte) (*Header, error) {
	if len(blockData) < v4SizePrefixLen {
		return nil, fmt.Errorf("v4 header block too short for size prefix: %d bytes", len(blockData))
	}

	decompressed, err := decompressLZ4(blockData[v4SizePrefixLen:])
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-decompress v4 header block: %w", err)
	}

	reader := bytes.NewReader(decompressed)

	selfID, self, err := deserializeDependency(reader)
	if err != nil {
		return nil, fmt.Errorf("read self dep: %w", err)
	}
	if selfID != metadata.BuildId {
		return nil, fmt.Errorf("self dep build id mismatch: got %s, expected %s", selfID, metadata.BuildId)
	}

	var n uint32
	if err := binary.Read(reader, binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("failed to read parent dependency count: %w", err)
	}

	parent := make(map[uuid.UUID]Dependency, n)
	for range n {
		id, dep, err := deserializeDependency(reader)
		if err != nil {
			return nil, fmt.Errorf("read parent dep: %w", err)
		}
		parent[id] = dep
	}

	var numMappings uint32
	if err := binary.Read(reader, binary.LittleEndian, &numMappings); err != nil {
		return nil, fmt.Errorf("failed to read mappings count: %w", err)
	}

	mappings := make([]BuildMap, 0, numMappings)
	for range numMappings {
		var v4 v4SerializableBuildMap
		if err := binary.Read(reader, binary.LittleEndian, &v4); err != nil {
			return nil, fmt.Errorf("failed to read block mapping: %w", err)
		}

		mappings = append(mappings, BuildMap{
			Offset:             v4.Offset,
			Length:             v4.Length,
			BuildId:            v4.BuildId,
			BuildStorageOffset: v4.BuildStorageOffset,
		})
	}

	return NewHeaderWithResolvedDependencies(metadata, mappings, self, parent)
}

func compressLZ4(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(data))

	w := lz4.NewWriter(&buf)
	if err := w.Apply(
		lz4.BlockSizeOption(lz4.Block4Mb),
		lz4.BlockChecksumOption(true),
		lz4.ChecksumOption(true),
		lz4.CompressionLevelOption(lz4.Fast),
	); err != nil {
		return nil, fmt.Errorf("lz4 options: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("lz4 compress: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("lz4 compress close: %w", err)
	}

	return buf.Bytes(), nil
}

func extractRelevantRanges(mappings []BuildMap) map[uuid.UUID][]storage.Range {
	ranges := make(map[uuid.UUID][]storage.Range)
	for _, m := range mappings {
		ranges[m.BuildId] = append(ranges[m.BuildId], storage.Range{
			Offset: int64(m.BuildStorageOffset),
			Length: int(m.Length),
		})
	}

	return ranges
}

func decompressLZ4(src []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(src))

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}

	return data, nil
}

func serializeDependency(w *bytes.Buffer, id uuid.UUID, dep Dependency) error {
	s := v4SerializableDependency{
		BuildId:  id,
		FileSize: dep.Size,
		Checksum: dep.Checksum,
	}
	if err := binary.Write(w, binary.LittleEndian, &s); err != nil {
		return fmt.Errorf("write dependency info: %w", err)
	}
	if err := dep.FrameTable.Serialize(w); err != nil {
		return fmt.Errorf("write dependency frame data: %w", err)
	}

	return nil
}

func deserializeDependency(r io.Reader) (uuid.UUID, Dependency, error) {
	var entry v4SerializableDependency
	if err := binary.Read(r, binary.LittleEndian, &entry); err != nil {
		return uuid.Nil, Dependency{}, fmt.Errorf("read dependency info: %w", err)
	}
	ft, err := storage.DeserializeFrameTable(r)
	if err != nil {
		return uuid.Nil, Dependency{}, fmt.Errorf("read frame table for dependency %s: %w", entry.BuildId, err)
	}

	return entry.BuildId, Dependency{
		Size:       entry.FileSize,
		Checksum:   entry.Checksum,
		FrameTable: ft,
	}, nil
}
