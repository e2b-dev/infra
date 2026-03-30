package header

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/google/uuid"
	lz4 "github.com/pierrec/lz4/v4"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	// metadataVersion is used by template-manager for uncompressed builds (V3 headers).
	metadataVersion = 3
	// MetadataVersionCompressed is used for compressed builds (V4 headers with FrameTables).
	MetadataVersionCompressed = 4
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
		Version:     m.Version,
		Generation:  m.Generation + 1,
		BlockSize:   m.BlockSize,
		Size:        m.Size,
		BuildId:     buildID,
		BaseBuildId: m.BaseBuildId,
	}
}

// v4SerializableBuildFileInfo is the on-disk format for a BuildFileInfo entry.
type v4SerializableBuildFileInfo struct {
	BuildId  uuid.UUID
	Size     int64
	Checksum [32]byte
}

func serialize(metadata *Metadata, buildFiles map[uuid.UUID]BuildFileInfo, mappings []*BuildMap) ([]byte, error) {
	var buf bytes.Buffer

	err := binary.Write(&buf, binary.LittleEndian, metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	if metadata.Version >= 4 {
		// V4: write build-info section before mappings.
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(buildFiles))); err != nil {
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
			if err := binary.Write(&buf, binary.LittleEndian, &entry); err != nil {
				return nil, fmt.Errorf("failed to write build file info: %w", err)
			}
		}

		// V4: write mapping count before mappings.
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(mappings))); err != nil {
			return nil, fmt.Errorf("failed to write mappings count: %w", err)
		}
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
				v4.CompressionTypeNumFrames = uint64(mapping.FrameTable.CompressionType())<<24 | uint64(len(mapping.FrameTable.Frames)&0xFFFFFF)
				// Only write offset/frames when the packed value is non-zero,
				// matching the deserializer's condition. A FrameTable with
				// CompressionNone and zero frames produces a packed value of 0.
				if v4.CompressionTypeNumFrames != 0 {
					offset = &mapping.FrameTable.StartAt
					frames = mapping.FrameTable.Frames
				}
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

// metadataSize is the binary size of the Metadata struct, computed from the struct layout.
var metadataSize = binary.Size(Metadata{})

func deserializeMetadata(data []byte) (*Metadata, error) {
	var metadata Metadata

	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	return &metadata, nil
}

// deserializeV3Mappings reads V3 mappings until EOF.
func deserializeV3Mappings(reader *bytes.Reader) ([]*BuildMap, error) {
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

	return mappings, nil
}

// deserializeV4Block reads the V4 block: build-info section, then counted mappings.
func deserializeV4Block(reader *bytes.Reader) (map[uuid.UUID]BuildFileInfo, []*BuildMap, error) {
	// Read build-info section.
	var numBuilds uint32
	if err := binary.Read(reader, binary.LittleEndian, &numBuilds); err != nil {
		return nil, nil, fmt.Errorf("failed to read build files count: %w", err)
	}

	var buildFiles map[uuid.UUID]BuildFileInfo
	if numBuilds > 0 {
		buildFiles = make(map[uuid.UUID]BuildFileInfo, numBuilds)
		for range numBuilds {
			var entry v4SerializableBuildFileInfo
			if err := binary.Read(reader, binary.LittleEndian, &entry); err != nil {
				return nil, nil, fmt.Errorf("failed to read build file info: %w", err)
			}
			buildFiles[entry.BuildId] = BuildFileInfo{
				Size:     entry.Size,
				Checksum: entry.Checksum,
			}
		}
	}

	// Read counted mappings.
	var numMappings uint32
	if err := binary.Read(reader, binary.LittleEndian, &numMappings); err != nil {
		return nil, nil, fmt.Errorf("failed to read mappings count: %w", err)
	}

	mappings := make([]*BuildMap, 0, numMappings)
	for range numMappings {
		var v4 v4SerializableBuildMap
		if err := binary.Read(reader, binary.LittleEndian, &v4); err != nil {
			return nil, nil, fmt.Errorf("failed to read block mapping: %w", err)
		}

		m := &BuildMap{
			Offset:             v4.Offset,
			Length:             v4.Length,
			BuildId:            v4.BuildId,
			BuildStorageOffset: v4.BuildStorageOffset,
		}

		if v4.CompressionTypeNumFrames != 0 {
			m.FrameTable = storage.NewFrameTable(storage.CompressionType((v4.CompressionTypeNumFrames >> 24) & 0xFF))
			numFrames := v4.CompressionTypeNumFrames & 0xFFFFFF

			var startAt storage.FrameOffset
			if err := binary.Read(reader, binary.LittleEndian, &startAt); err != nil {
				return nil, nil, fmt.Errorf("failed to read compression frames starting offset: %w", err)
			}
			m.FrameTable.StartAt = startAt

			for range numFrames {
				var frame storage.FrameSize
				if err := binary.Read(reader, binary.LittleEndian, &frame); err != nil {
					return nil, nil, fmt.Errorf("failed to read the expected compression frame: %w", err)
				}
				m.FrameTable.Frames = append(m.FrameTable.Frames, frame)
			}
		}

		mappings = append(mappings, m)
	}

	return buildFiles, mappings, nil
}

// Serialize serializes a V3 header from metadata and mappings (legacy API).
func Serialize(metadata *Metadata, mappings []*BuildMap) ([]byte, error) {
	return serialize(metadata, nil, mappings)
}

// SerializeHeader serializes a header with optional LZ4 compression for V4.
//
// V3 (Version <= 3): [Metadata (raw binary)] [v3 mappings (raw binary)]
//
// V4 (Version >= 4):  [Metadata (raw binary)] [uint32 uncompressed block size] [LZ4-compressed block]
//
//	where the LZ4 block contains: BuildFiles + v4 mappings with FrameTables.
func SerializeHeader(h *Header) ([]byte, error) {
	raw, err := serialize(h.Metadata, h.BuildFiles, h.Mapping)
	if err != nil {
		return nil, err
	}

	if h.Metadata.Version <= 3 {
		return raw, nil
	}

	// V4: keep Metadata prefix raw, then [uint32 uncompressed size] + [LZ4 frame].
	block := raw[metadataSize:]
	compressed, err := compressLZ4(block)
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-compress v4 header mappings: %w", err)
	}

	result := make([]byte, metadataSize+4+len(compressed))
	copy(result, raw[:metadataSize])
	binary.LittleEndian.PutUint32(result[metadataSize:], uint32(len(block)))
	copy(result[metadataSize+4:], compressed)

	return result, nil
}

// LoadHeader fetches a serialized header from storage and deserializes it.
// Errors (including storage.ErrObjectNotExist) are returned as-is.
func LoadHeader(ctx context.Context, s storage.StorageProvider, path string) (*Header, error) {
	blob, err := s.OpenBlob(ctx, path) // TODO: restore storage.MetadataObjectType param
	if err != nil {
		return nil, fmt.Errorf("open blob %s: %w", path, err)
	}

	data, err := storage.GetBlob(ctx, blob)
	if err != nil {
		return nil, err
	}

	return DeserializeBytes(data)
}

// StoreHeader serializes a header and uploads it to storage.
// Inverse of LoadHeader.
func StoreHeader(ctx context.Context, s storage.StorageProvider, path string, h *Header) error {
	data, err := SerializeHeader(h)
	if err != nil {
		return fmt.Errorf("serialize header: %w", err)
	}

	blob, err := s.OpenBlob(ctx, path) // TODO: restore storage.MetadataObjectType param
	if err != nil {
		return fmt.Errorf("open blob %s: %w", path, err)
	}

	return blob.Put(ctx, data)
}

// Deserialize reads a header from a storage Blob (legacy API).
func Deserialize(ctx context.Context, in storage.Blob) (*Header, error) {
	data, err := storage.GetBlob(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("failed to write to buffer: %w", err)
	}

	return DeserializeBytes(data)
}

// DeserializeBytes auto-detects the header version and deserializes accordingly.
// See SerializeHeader for the binary layout.
// The uint32 size prefix in V4 allows exact-size allocation for decompression
// instead of a fixed upper-bound buffer.
func DeserializeBytes(data []byte) (*Header, error) {
	if len(data) < metadataSize {
		return nil, fmt.Errorf("header too short: %d bytes", len(data))
	}

	metadata, err := deserializeMetadata(data[:metadataSize])
	if err != nil {
		return nil, err
	}

	blockData := data[metadataSize:]

	if metadata.Version >= 4 {
		if len(blockData) < 4 {
			return nil, fmt.Errorf("v4 header block too short for size prefix: %d bytes", len(blockData))
		}

		uncompressedSize := binary.LittleEndian.Uint32(blockData[:4])
		if uncompressedSize > storage.MaxCompressedHeaderSize {
			return nil, fmt.Errorf("v4 header uncompressed size %d exceeds maximum %d", uncompressedSize, storage.MaxCompressedHeaderSize)
		}

		blockData, err = decompressLZ4(blockData[4:])
		if err != nil {
			return nil, fmt.Errorf("failed to LZ4-decompress v4 header block: %w", err)
		}

		buildFiles, mappings, err := deserializeV4Block(bytes.NewReader(blockData))
		if err != nil {
			return nil, err
		}

		h, err := NewHeader(metadata, mappings)
		if err != nil {
			return nil, err
		}
		h.BuildFiles = buildFiles

		return h, nil
	}

	mappings, err := deserializeV3Mappings(bytes.NewReader(blockData))
	if err != nil {
		return nil, err
	}

	return NewHeader(metadata, mappings)
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
