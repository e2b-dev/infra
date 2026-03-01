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
)

const (
	// metadataVersion is used by template-manager for uncompressed builds (V3 headers).
	metadataVersion = 3
	// MetadataVersionCompressed is used by compress-build for compressed builds (V4 headers with FrameTables).
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
		for id, info := range buildFiles {
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
				v4.CompressionTypeNumFrames = uint64(mapping.FrameTable.CompressionType)<<24 | uint64(len(mapping.FrameTable.Frames))
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

// FromBlob reads all bytes from a storage.Blob and auto-detects
// the header version (V3/V4) for deserialization.
func FromBlob(ctx context.Context, in storage.Blob) (*Header, error) {
	data, err := storage.GetBlob(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("failed to write to buffer: %w", err)
	}

	return Deserialize(data)
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
			m.FrameTable = &storage.FrameTable{
				CompressionType: storage.CompressionType((v4.CompressionTypeNumFrames >> 24) & 0xFF),
			}
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

// Serialize serializes a header with optional LZ4 compression for V4.
// For V3 (Version <= 3), returns the raw binary unchanged (BuildFiles ignored).
// For V4 (Version == 4), keeps Metadata prefix raw, LZ4-compresses
// the rest (build info + mappings with frame tables), and concatenates.
func Serialize(h *Header) ([]byte, error) {
	raw, err := serialize(h.Metadata, h.BuildFiles, h.Mapping)
	if err != nil {
		return nil, err
	}

	if h.Metadata.Version <= 3 {
		return raw, nil
	}

	// V4: keep Metadata prefix raw, LZ4-compress the rest.
	compressed, err := storage.CompressLZ4(raw[metadataSize:])
	if err != nil {
		return nil, fmt.Errorf("failed to LZ4-compress v4 header mappings: %w", err)
	}

	result := make([]byte, metadataSize+len(compressed))
	copy(result, raw[:metadataSize])
	copy(result[metadataSize:], compressed)

	return result, nil
}

// LoadHeader fetches a serialized header from storage and deserializes it.
// Errors (including storage.ErrObjectNotExist) are returned as-is.
func LoadHeader(ctx context.Context, s storage.StorageProvider, path string) (*Header, error) {
	data, err := storage.LoadBlob(ctx, s, path)
	if err != nil {
		return nil, err
	}

	return Deserialize(data)
}

// StoreHeader serializes a header and uploads it to storage.
// Inverse of LoadHeader.
func StoreHeader(ctx context.Context, s storage.StorageProvider, path string, h *Header) error {
	data, err := Serialize(h)
	if err != nil {
		return fmt.Errorf("serialize header: %w", err)
	}

	blob, err := s.OpenBlob(ctx, path)
	if err != nil {
		return fmt.Errorf("open blob %s: %w", path, err)
	}

	return blob.Put(ctx, data)
}

// Deserialize auto-detects the header version and deserializes accordingly.
// For V3 (Version <= 3), deserializes the raw binary directly.
// For V4 (Version == 4), reads the Metadata prefix, then LZ4-decompresses
// the remaining bytes (build info + mappings with frame tables) and deserializes them.
func Deserialize(data []byte) (*Header, error) {
	if len(data) < metadataSize {
		return nil, fmt.Errorf("header too short: %d bytes", len(data))
	}

	metadata, err := deserializeMetadata(data[:metadataSize])
	if err != nil {
		return nil, err
	}

	blockData := data[metadataSize:]

	if metadata.Version >= 4 {
		blockData, err = storage.DecompressLZ4(blockData, storage.MaxCompressedHeaderSize)
		if err != nil {
			return nil, fmt.Errorf("failed to LZ4-decompress v4 header block: %w", err)
		}

		buildFiles, mappings, err := deserializeV4Block(bytes.NewReader(blockData))
		if err != nil {
			return nil, err
		}

		h, err := newValidatedHeader(metadata, mappings)
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

	return newValidatedHeader(metadata, mappings)
}

func newValidatedHeader(metadata *Metadata, mappings []*BuildMap) (*Header, error) {
	header, err := NewHeader(metadata, mappings)
	if err != nil {
		return nil, err
	}

	if err := ValidateHeader(header); err != nil {
		return nil, fmt.Errorf("header validation failed: %w", err)
	}

	return header, nil
}
