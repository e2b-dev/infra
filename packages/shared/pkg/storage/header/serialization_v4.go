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

// v4SizePrefixLen is the length of the uint32 size prefix that precedes the
// LZ4-compressed block in the V4 header layout.
const v4SizePrefixLen = 4

// v4FlagsLen is the length of the V4 flags byte. Bit 0 = IncompletePendingUpload.
const v4FlagsLen = 1

// v4FlagIncomplete is bit 0 of the V4 flags byte: when set, the header
// describes a build whose upload has not yet finalized (an in-flight diff).
// StoreHeader refuses to persist headers carrying this flag; only the P2P
// peer-server path emits it.
const v4FlagIncomplete uint8 = 1 << 0

type v4SerializableBuildMap struct {
	Offset             uint64
	Length             uint64
	BuildId            [16]byte // uuid.UUID
	BuildStorageOffset uint64
}

// v4SerializableBuildInfo is the on-disk format for a build's fixed fields,
// followed by a serialized FrameTable.
type v4SerializableBuildInfo struct {
	BuildId  uuid.UUID
	FileSize int64
	Checksum [32]byte
}

// serializeV4 writes [Metadata] [uint8 flags] [uint32 LZ4 size] [LZ4( Builds[] + Mappings[] )].
// Frame tables are sparse-trimmed to only frames referenced by mappings.
func serializeV4(metadata *Metadata, builds map[uuid.UUID]BuildData, mappings []BuildMap, incomplete bool) ([]byte, error) {
	var metaBuf bytes.Buffer
	if err := binary.Write(&metaBuf, binary.LittleEndian, metadata); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	var block bytes.Buffer

	// Sort by UUID for deterministic serialization.
	buildIDs := make([]uuid.UUID, 0, len(builds))
	for id := range builds {
		buildIDs = append(buildIDs, id)
	}
	slices.SortFunc(buildIDs, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})

	if err := binary.Write(&block, binary.LittleEndian, uint32(len(buildIDs))); err != nil {
		return nil, fmt.Errorf("failed to write build count: %w", err)
	}

	buildRanges := extractRelevantRanges(mappings)
	for _, id := range buildIDs {
		bd := builds[id]

		entry := v4SerializableBuildInfo{
			BuildId:  id,
			FileSize: bd.Size,
			Checksum: bd.Checksum,
		}

		if err := binary.Write(&block, binary.LittleEndian, &entry); err != nil {
			return nil, fmt.Errorf("failed to write build info: %w", err)
		}

		trimmed := bd.FrameData.TrimToRanges(buildRanges[id])
		if err := trimmed.Serialize(&block); err != nil {
			return nil, fmt.Errorf("failed to write build frame data: %w", err)
		}
	}

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

		if err := binary.Write(&block, binary.LittleEndian, v4); err != nil {
			return nil, fmt.Errorf("failed to write block mapping: %w", err)
		}
	}

	// LZ4-compress the block and assemble: [metadata] [uint8 flags] [uint32 size] [compressed block].
	blockBytes := block.Bytes()
	compressed, err := compressLZ4(blockBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-compress v4 header block: %w", err)
	}

	var flags uint8
	if incomplete {
		flags |= v4FlagIncomplete
	}

	result := make([]byte, metadataSize+v4FlagsLen+v4SizePrefixLen+len(compressed))
	copy(result, metaBuf.Bytes())
	result[metadataSize] = flags
	binary.LittleEndian.PutUint32(result[metadataSize+v4FlagsLen:], uint32(len(blockBytes)))
	copy(result[metadataSize+v4FlagsLen+v4SizePrefixLen:], compressed)

	return result, nil
}

// deserializeV4 decompresses and reads the V4 block.
func deserializeV4(metadata *Metadata, blockData []byte) (*Header, error) {
	if len(blockData) < v4FlagsLen+v4SizePrefixLen {
		return nil, fmt.Errorf("v4 header block too short for flags + size prefix: %d bytes", len(blockData))
	}

	flags := blockData[0]

	decompressed, err := decompressLZ4(blockData[v4FlagsLen+v4SizePrefixLen:])
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-decompress v4 header block: %w", err)
	}

	reader := bytes.NewReader(decompressed)

	var numBuilds uint32
	if err := binary.Read(reader, binary.LittleEndian, &numBuilds); err != nil {
		return nil, fmt.Errorf("failed to read build count: %w", err)
	}

	var builds map[uuid.UUID]BuildData

	if numBuilds > 0 {
		builds = make(map[uuid.UUID]BuildData, numBuilds)

		for range numBuilds {
			var entry v4SerializableBuildInfo
			if err := binary.Read(reader, binary.LittleEndian, &entry); err != nil {
				return nil, fmt.Errorf("failed to read build info: %w", err)
			}

			bd := BuildData{
				Size:     entry.FileSize,
				Checksum: entry.Checksum,
			}

			ft, err := storage.DeserializeFrameTable(reader)
			if err != nil {
				return nil, fmt.Errorf("failed to read frame table for build %s: %w", entry.BuildId, err)
			}

			bd.FrameData = ft
			builds[entry.BuildId] = bd
		}
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

		m := BuildMap{
			Offset:             v4.Offset,
			Length:             v4.Length,
			BuildId:            v4.BuildId,
			BuildStorageOffset: v4.BuildStorageOffset,
		}

		mappings = append(mappings, m)
	}

	h, err := NewHeader(metadata, mappings)
	if err != nil {
		return nil, err
	}
	h.Builds = builds
	h.IncompletePendingUpload = flags&v4FlagIncomplete != 0

	return h, nil
}

// compressLZ4 compresses data for V4 header serialization using the LZ4
// streaming API. Settings are fixed for the V4 wire format.
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

// extractRelevantRanges groups mappings into per-build U-space [start, end) ranges
// for sparse frame table trimming during serialization.
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

// decompressLZ4 decompresses an LZ4 frame from V4 header data.
func decompressLZ4(src []byte) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(src))

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}

	return data, nil
}
