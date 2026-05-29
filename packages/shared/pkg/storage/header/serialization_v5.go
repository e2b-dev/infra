package header

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

// V5 keeps V4's framing — [Metadata][uint8 flags][uint32 uncompressedSize]
// [LZ4(block)] — and an identical Builds section. Only the mapping section
// changes: instead of N fixed 40-byte records, it is columnar and varint-coded.
//
// V4 mapping bytes are dominated by the offset/length/storage columns (three
// 8-byte little-endian integers each, mostly tiny or monotonic) and by a full
// 16-byte BuildId repeated on every entry. For page-granular memfile dedup a
// single header can hold millions of entries, blowing past the 64 MiB
// uncompressed cap and compressing poorly (interleaved unique 8-byte values
// defeat LZ4). V5 fixes both:
//
//   - block-indexed columns (offset/length/storage as deltas/values in block
//     units, varint-coded): tiny per entry, and storing each column
//     contiguously lets LZ4 collapse the highly regular runs.
//   - a per-header build-id table addressed by a varint index, so a 16-byte
//     UUID is stored once per distinct build instead of once per entry.

// serializeV5 mirrors serializeV4 but writes the columnar mapping section.
// Returns the assembled bytes and the uncompressed inner-block size.
func serializeV5(metadata *Metadata, builds map[uuid.UUID]BuildData, mapping Mapping, incomplete bool) ([]byte, int64, error) {
	var metaBuf bytes.Buffer
	if err := binary.Write(&metaBuf, binary.LittleEndian, metadata); err != nil {
		return nil, 0, fmt.Errorf("failed to write metadata: %w", err)
	}

	var block bytes.Buffer

	if err := writeV4BuildsSection(&block, builds, mapping); err != nil {
		return nil, 0, err
	}

	if err := writeV5MappingSection(&block, mapping); err != nil {
		return nil, 0, err
	}

	blockBytes := block.Bytes()
	compressed, err := compressLZ4(blockBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to LZ4-compress v5 header block: %w", err)
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

// writeV5MappingSection writes the columnar, varint-coded mapping:
//
//	uint32 N                       entry count
//	uint32 M                       distinct non-empty build-id count
//	M × [16]byte                   build-id table
//	N × uvarint                    offset-block deltas (offset[i]-offset[i-1])
//	N × uvarint                    length in blocks
//	N × varint  (zig-zag)          storage-block deltas (storage[i]-storage[i-1])
//	N × uvarint                    build-id table index (M = empty)
func writeV5MappingSection(block *bytes.Buffer, m Mapping) error {
	n := len(m.offsets)
	if err := binary.Write(block, binary.LittleEndian, uint32(n)); err != nil {
		return fmt.Errorf("failed to write mapping count: %w", err)
	}
	if err := binary.Write(block, binary.LittleEndian, uint32(len(m.builds))); err != nil {
		return fmt.Errorf("failed to write mapping build count: %w", err)
	}
	for _, id := range m.builds {
		if _, err := block.Write(id[:]); err != nil {
			return fmt.Errorf("failed to write mapping build id: %w", err)
		}
	}

	var buf [binary.MaxVarintLen64]byte

	var prevOffset uint64
	for _, v := range m.offsets {
		off := uint64(v)
		block.Write(buf[:binary.PutUvarint(buf[:], off-prevOffset)])
		prevOffset = off
	}
	for _, v := range m.lengths {
		block.Write(buf[:binary.PutUvarint(buf[:], uint64(v))])
	}
	var prevStorage int64
	for _, v := range m.storage {
		s := int64(v)
		block.Write(buf[:binary.PutVarint(buf[:], s-prevStorage)])
		prevStorage = s
	}
	for _, v := range m.buildIdx {
		idx := uint64(v)
		if v == nilBuildIdx {
			idx = uint64(len(m.builds))
		}
		block.Write(buf[:binary.PutUvarint(buf[:], idx)])
	}

	return nil
}

// deserializeV5 decompresses and reads the V5 block.
func deserializeV5(metadata *Metadata, blockData []byte) (*Header, error) {
	if metadata.BlockSize == 0 {
		return nil, errors.New("v5 header has zero block size")
	}
	if len(blockData) < v4FlagsLen+v4SizePrefixLen {
		return nil, fmt.Errorf("v5 header block too short for flags + size prefix: %d bytes", len(blockData))
	}

	flags := blockData[0]
	size := binary.LittleEndian.Uint32(blockData[v4FlagsLen:])
	if uint64(size) > uint64(v4MaxUncompressedHeaderSize) {
		return nil, fmt.Errorf("v5 header uncompressed size %d exceeds cap %d", size, v4MaxUncompressedHeaderSize)
	}

	decompressed, err := decompressLZ4(blockData[v4FlagsLen+v4SizePrefixLen:], int(size))
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-decompress v5 header block: %w", err)
	}
	if len(decompressed) != int(size) {
		return nil, fmt.Errorf("v5 header decompressed size %d != prefix %d", len(decompressed), size)
	}

	reader := bytes.NewReader(decompressed)

	builds, err := readV4BuildsSection(reader)
	if err != nil {
		return nil, err
	}

	// The compact mapping is encoded in PageSize units (see NewHeader), not the
	// file's logical block size, so decode it the same way.
	mapping, err := readV5MappingSection(reader, PageSize)
	if err != nil {
		return nil, err
	}

	h := &Header{
		Metadata:                metadata,
		Builds:                  builds,
		Mapping:                 mapping,
		IncompletePendingUpload: flags&v4FlagIncomplete != 0,
	}

	return h, nil
}

// readV5MappingSection reads the columnar mapping written by
// writeV5MappingSection and reconstructs the compact Mapping directly.
func readV5MappingSection(reader *bytes.Reader, blockSize uint64) (Mapping, error) {
	var n, m uint32
	if err := binary.Read(reader, binary.LittleEndian, &n); err != nil {
		return Mapping{}, fmt.Errorf("failed to read mapping count: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &m); err != nil {
		return Mapping{}, fmt.Errorf("failed to read mapping build count: %w", err)
	}
	if m > maxBuildsPerHeader {
		return Mapping{}, fmt.Errorf("mapping build count %d exceeds maximum %d", m, maxBuildsPerHeader)
	}

	builds := make([]uuid.UUID, m)
	for i := range builds {
		if _, err := io.ReadFull(reader, builds[i][:]); err != nil {
			return Mapping{}, fmt.Errorf("failed to read mapping build id %d: %w", i, err)
		}
	}

	if n == 0 {
		return newMappingFromColumns(blockSize, builds, nil, nil, nil, nil)
	}
	// Each entry needs at least one byte in each of the four columns. Bound the
	// allocation against a crafted count before allocating the column slices.
	if uint64(n) > uint64(reader.Len())/4 {
		return Mapping{}, fmt.Errorf("mapping count %d exceeds remaining %d bytes", n, reader.Len())
	}

	offsets := make([]uint32, n)
	lengths := make([]uint32, n)
	storageCol := make([]uint32, n)
	buildIdx := make([]uint16, n)

	var prevOffset uint64
	for i := range offsets {
		d, err := binary.ReadUvarint(reader)
		if err != nil {
			return Mapping{}, fmt.Errorf("failed to read offset delta %d: %w", i, err)
		}
		// Bound the delta before adding so the running sum can't overflow uint64
		// and wrap past the maxBlockIdx check.
		if d > maxBlockIdx {
			return Mapping{}, fmt.Errorf("offset delta %d at entry %d exceeds uint32", d, i)
		}
		prevOffset += d
		if prevOffset > maxBlockIdx {
			return Mapping{}, fmt.Errorf("offset block %d at entry %d exceeds uint32", prevOffset, i)
		}
		offsets[i] = uint32(prevOffset)
	}
	for i := range lengths {
		v, err := binary.ReadUvarint(reader)
		if err != nil {
			return Mapping{}, fmt.Errorf("failed to read length %d: %w", i, err)
		}
		if v > maxBlockIdx {
			return Mapping{}, fmt.Errorf("length block %d at entry %d exceeds uint32", v, i)
		}
		lengths[i] = uint32(v)
	}
	var prevStorage int64
	for i := range storageCol {
		d, err := binary.ReadVarint(reader)
		if err != nil {
			return Mapping{}, fmt.Errorf("failed to read storage delta %d: %w", i, err)
		}
		prevStorage += d
		if prevStorage < 0 || prevStorage > maxBlockIdx {
			return Mapping{}, fmt.Errorf("storage block %d at entry %d out of range", prevStorage, i)
		}
		storageCol[i] = uint32(prevStorage)
	}
	for i := range buildIdx {
		v, err := binary.ReadUvarint(reader)
		if err != nil {
			return Mapping{}, fmt.Errorf("failed to read build index %d: %w", i, err)
		}
		if v > uint64(m) {
			return Mapping{}, fmt.Errorf("build index %d at entry %d out of range (%d builds)", v, i, m)
		}
		if v == uint64(m) {
			buildIdx[i] = nilBuildIdx
		} else {
			buildIdx[i] = uint16(v)
		}
	}

	return newMappingFromColumns(blockSize, builds, offsets, lengths, storageCol, buildIdx)
}
