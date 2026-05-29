package header

import (
	"bytes"
	"encoding/binary"
	"errors"
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

// v4MaxUncompressedHeaderSize caps the uncompressed V4 header block as an
// anti-decompression-bomb guard (decompressLZ4 keeps the actual overrun bound).
// Raised from 64 MiB to 256 MiB: a page-granular memfile diff can legitimately
// produce a header above 64 MiB, and the old cap rejected such headers only on
// read, permanently stranding already-uploaded snapshots whose data files are
// intact. A var (not const) so tests can lower it cheaply.
var v4MaxUncompressedHeaderSize = 256 << 20

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

var (
	v4BuildInfoSize      = binary.Size(v4SerializableBuildInfo{})
	v4MappingSize        = binary.Size(v4SerializableBuildMap{})
	frameTableHeaderSize = 2 * binary.Size(uint32(0))
)

// serializeV4 writes [Metadata] [uint8 flags] [uint32 LZ4 size] [LZ4( Builds[] + Mappings[] )].
// Frame tables are sparse-trimmed to only frames referenced by mappings.
// Also returns the uncompressed inner-block size (LZ4 input length).
func serializeV4(metadata *Metadata, builds map[uuid.UUID]BuildData, mappings Mapping, incomplete bool) ([]byte, int64, error) {
	var metaBuf bytes.Buffer
	if err := binary.Write(&metaBuf, binary.LittleEndian, metadata); err != nil {
		return nil, 0, fmt.Errorf("failed to write metadata: %w", err)
	}

	var block bytes.Buffer

	if err := writeV4BuildsSection(&block, builds, mappings); err != nil {
		return nil, 0, err
	}

	if err := binary.Write(&block, binary.LittleEndian, uint32(mappings.Len())); err != nil {
		return nil, 0, fmt.Errorf("failed to write mappings count: %w", err)
	}

	for _, mapping := range mappings.All() {
		v4 := &v4SerializableBuildMap{
			Offset:             mapping.Offset,
			Length:             mapping.Length,
			BuildId:            mapping.BuildId,
			BuildStorageOffset: mapping.BuildStorageOffset,
		}

		if err := binary.Write(&block, binary.LittleEndian, v4); err != nil {
			return nil, 0, fmt.Errorf("failed to write block mapping: %w", err)
		}
	}

	// LZ4-compress the block and assemble: [metadata] [uint8 flags] [uint32 size] [compressed block].
	blockBytes := block.Bytes()
	compressed, err := compressLZ4(blockBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to LZ4-compress v4 header block: %w", err)
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

	return result, int64(len(blockBytes)), nil
}

// deserializeV4 decompresses and reads the V4 block.
func deserializeV4(metadata *Metadata, blockData []byte) (*Header, error) {
	if len(blockData) < v4FlagsLen+v4SizePrefixLen {
		return nil, fmt.Errorf("v4 header block too short for flags + size prefix: %d bytes", len(blockData))
	}

	flags := blockData[0]
	size := binary.LittleEndian.Uint32(blockData[v4FlagsLen:])
	if uint64(size) > uint64(v4MaxUncompressedHeaderSize) {
		return nil, fmt.Errorf("v4 header uncompressed size %d exceeds cap %d", size, v4MaxUncompressedHeaderSize)
	}

	decompressed, err := decompressLZ4(blockData[v4FlagsLen+v4SizePrefixLen:], int(size))
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-decompress v4 header block: %w", err)
	}
	if len(decompressed) != int(size) {
		return nil, fmt.Errorf("v4 header decompressed size %d != prefix %d", len(decompressed), size)
	}

	reader := bytes.NewReader(decompressed)

	builds, err := readV4BuildsSection(reader)
	if err != nil {
		return nil, err
	}

	var numMappings uint32
	if err := binary.Read(reader, binary.LittleEndian, &numMappings); err != nil {
		return nil, fmt.Errorf("failed to read mappings count: %w", err)
	}
	if uint64(numMappings) > uint64(reader.Len())/uint64(v4MappingSize) {
		return nil, fmt.Errorf("mapping count %d exceeds remaining %d bytes", numMappings, reader.Len())
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

// writeV4BuildsSection writes the Builds section shared by V4 and V5:
// [uint32 count] then, per build (sorted by UUID for determinism), the fixed
// info plus a frame table sparse-trimmed to the ranges referenced by mapping.
func writeV4BuildsSection(block *bytes.Buffer, builds map[uuid.UUID]BuildData, mapping Mapping) error {
	buildIDs := make([]uuid.UUID, 0, len(builds))
	for id := range builds {
		buildIDs = append(buildIDs, id)
	}
	slices.SortFunc(buildIDs, func(a, b uuid.UUID) int {
		return bytes.Compare(a[:], b[:])
	})

	if err := binary.Write(block, binary.LittleEndian, uint32(len(buildIDs))); err != nil {
		return fmt.Errorf("failed to write build count: %w", err)
	}

	buildRanges := extractRelevantRanges(mapping)
	for _, id := range buildIDs {
		bd := builds[id]

		entry := v4SerializableBuildInfo{
			BuildId:  id,
			FileSize: bd.Size,
			Checksum: bd.Checksum,
		}
		if err := binary.Write(block, binary.LittleEndian, &entry); err != nil {
			return fmt.Errorf("failed to write build info: %w", err)
		}

		trimmed := bd.FrameData.TrimToRanges(buildRanges[id])
		if err := trimmed.Serialize(block); err != nil {
			return fmt.Errorf("failed to write build frame data: %w", err)
		}
	}

	return nil
}

// readV4BuildsSection reads the Builds section shared by V4 and V5. Returns a
// nil map when the build count is zero.
func readV4BuildsSection(reader *bytes.Reader) (map[uuid.UUID]BuildData, error) {
	var numBuilds uint32
	if err := binary.Read(reader, binary.LittleEndian, &numBuilds); err != nil {
		return nil, fmt.Errorf("failed to read build count: %w", err)
	}
	if numBuilds == 0 {
		return nil, nil
	}
	minBuildBytes := v4BuildInfoSize + frameTableHeaderSize
	if uint64(numBuilds) > uint64(reader.Len())/uint64(minBuildBytes) {
		return nil, fmt.Errorf("build count %d exceeds remaining %d bytes", numBuilds, reader.Len())
	}

	builds := make(map[uuid.UUID]BuildData, numBuilds)
	for range numBuilds {
		var entry v4SerializableBuildInfo
		if err := binary.Read(reader, binary.LittleEndian, &entry); err != nil {
			return nil, fmt.Errorf("failed to read build info: %w", err)
		}

		ft, err := storage.DeserializeFrameTable(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to read frame table for build %s: %w", entry.BuildId, err)
		}

		builds[entry.BuildId] = BuildData{
			Size:      entry.FileSize,
			Checksum:  entry.Checksum,
			FrameData: ft,
		}
	}

	return builds, nil
}

// extractRelevantRanges groups mappings into per-build U-space [start, end) ranges
// for sparse frame table trimming during serialization.
func extractRelevantRanges(mappings Mapping) map[uuid.UUID][]storage.Range {
	ranges := make(map[uuid.UUID][]storage.Range)
	for _, m := range mappings.All() {
		ranges[m.BuildId] = append(ranges[m.BuildId], storage.Range{
			Offset: int64(m.BuildStorageOffset),
			Length: int(m.Length),
		})
	}

	return ranges
}

// decompressLZ4 reads up to expected+1 bytes; deserializeV4 rejects any
// overrun. Bound defends against LZ4's ~255x expansion on malformed input.
func decompressLZ4(src []byte, expected int) ([]byte, error) {
	r := lz4.NewReader(bytes.NewReader(src))

	buf := bytes.NewBuffer(make([]byte, 0, expected))
	if _, err := io.CopyN(buf, r, int64(expected)+1); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}

	return buf.Bytes(), nil
}
