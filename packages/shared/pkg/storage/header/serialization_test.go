package header

import (
	"crypto/rand"
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// newFT creates a FrameTable for test fixtures.
func newFT(ct storage.CompressionType, startAt storage.FrameOffset, frames []storage.FrameSize) *storage.FrameTable {
	ft := storage.NewFrameTable(ct)
	ft.StartAt = startAt
	ft.Frames = frames

	return ft
}

func TestSerializeDeserialize_V3_RoundTrip(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	baseID := uuid.New()
	metadata := &Metadata{
		Version:     3,
		BlockSize:   4096,
		Size:        8192,
		Generation:  7,
		BuildId:     buildID,
		BaseBuildId: baseID,
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 0,
		},
		{
			Offset:             4096,
			Length:             4096,
			BuildId:            baseID,
			BuildStorageOffset: 123,
		},
	}

	data, err := serialize(metadata, nil, mappings)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Equal(t, metadata, got.Metadata)
	require.Len(t, got.Mapping, 2)
	assert.Equal(t, uint64(0), got.Mapping[0].Offset)
	assert.Equal(t, uint64(4096), got.Mapping[0].Length)
	assert.Equal(t, buildID, got.Mapping[0].BuildId)
	assert.Equal(t, uint64(0), got.Mapping[0].BuildStorageOffset)

	assert.Equal(t, uint64(4096), got.Mapping[1].Offset)
	assert.Equal(t, uint64(4096), got.Mapping[1].Length)
	assert.Equal(t, baseID, got.Mapping[1].BuildId)
	assert.Equal(t, uint64(123), got.Mapping[1].BuildStorageOffset)

	// V3 headers have no BuildFiles
	assert.Nil(t, got.BuildFiles)
}

func TestDeserialize_TruncatedMetadata(t *testing.T) {
	t.Parallel()

	_, err := Deserialize([]byte{0x01, 0x02, 0x03})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "header too short")
}

func TestSerializeDeserialize_EmptyMappings_Defaults(t *testing.T) {
	t.Parallel()

	metadata := &Metadata{
		Version:     3,
		BlockSize:   4096,
		Size:        8192,
		Generation:  0,
		BuildId:     uuid.New(),
		BaseBuildId: uuid.New(),
	}

	data, err := serialize(metadata, nil, nil)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	// NewHeader creates a default mapping when none provided
	require.Len(t, got.Mapping, 1)
	assert.Equal(t, uint64(0), got.Mapping[0].Offset)
	assert.Equal(t, metadata.Size, got.Mapping[0].Length)
	assert.Equal(t, metadata.BuildId, got.Mapping[0].BuildId)
}

func TestDeserialize_BlockSizeZero(t *testing.T) {
	t.Parallel()

	metadata := &Metadata{
		Version:     3,
		BlockSize:   0,
		Size:        4096,
		Generation:  0,
		BuildId:     uuid.New(),
		BaseBuildId: uuid.New(),
	}

	data, err := serialize(metadata, nil, nil)
	require.NoError(t, err)

	_, err = Deserialize(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "block size cannot be zero")
}

func TestSerializeDeserialize_V4_WithFrameTable(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	baseID := uuid.New()
	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        8192,
		Generation:  1,
		BuildId:     buildID,
		BaseBuildId: baseID,
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 0,
			FrameTable: newFT(storage.CompressionLZ4, storage.FrameOffset{U: 0, C: 0}, []storage.FrameSize{
				{U: 2048, C: 1024},
				{U: 2048, C: 900},
			}),
		},
		{
			Offset:             4096,
			Length:             4096,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
	}

	checksum := sha256.Sum256([]byte("test-data"))
	buildFiles := map[uuid.UUID]BuildFileInfo{
		buildID: {Size: 12345, Checksum: checksum},
		baseID:  {Size: 67890},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	h.BuildFiles = buildFiles

	// Test with Serialize + Deserialize (unified path)
	data, err := Serialize(h)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Equal(t, uint64(4), got.Metadata.Version)
	require.Len(t, got.Mapping, 2)

	// First mapping has FrameTable
	m0 := got.Mapping[0]
	assert.Equal(t, uint64(0), m0.Offset)
	assert.Equal(t, uint64(4096), m0.Length)
	assert.Equal(t, buildID, m0.BuildId)
	require.NotNil(t, m0.FrameTable)
	assert.Equal(t, storage.CompressionLZ4, m0.FrameTable.CompressionType())
	assert.Equal(t, int64(0), m0.FrameTable.StartAt.U)
	assert.Equal(t, int64(0), m0.FrameTable.StartAt.C)
	require.Len(t, m0.FrameTable.Frames, 2)
	assert.Equal(t, int32(2048), m0.FrameTable.Frames[0].U)
	assert.Equal(t, int32(1024), m0.FrameTable.Frames[0].C)
	assert.Equal(t, int32(2048), m0.FrameTable.Frames[1].U)
	assert.Equal(t, int32(900), m0.FrameTable.Frames[1].C)

	// Second mapping has no FrameTable
	m1 := got.Mapping[1]
	assert.Equal(t, uint64(4096), m1.Offset)
	assert.Equal(t, uint64(4096), m1.Length)
	assert.Equal(t, baseID, m1.BuildId)
	assert.Nil(t, m1.FrameTable)

	// BuildFiles round-trip
	require.Len(t, got.BuildFiles, 2)
	assert.Equal(t, int64(12345), got.BuildFiles[buildID].Size)
	assert.Equal(t, checksum, got.BuildFiles[buildID].Checksum)
	assert.Equal(t, int64(67890), got.BuildFiles[baseID].Size)
	assert.Equal(t, [32]byte{}, got.BuildFiles[baseID].Checksum)
}

func TestSerializeDeserialize_V4_Zstd_NonZeroStartAt(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        4096,
		Generation:  0,
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 8192,
			FrameTable: newFT(storage.CompressionZstd, storage.FrameOffset{U: 8192, C: 4000}, []storage.FrameSize{
				{U: 4096, C: 3500},
			}),
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)

	// Test with Serialize + Deserialize (unified path)
	data, err := Serialize(h)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	m := got.Mapping[0]
	require.NotNil(t, m.FrameTable)
	assert.Equal(t, storage.CompressionZstd, m.FrameTable.CompressionType())
	assert.Equal(t, int64(8192), m.FrameTable.StartAt.U)
	assert.Equal(t, int64(4000), m.FrameTable.StartAt.C)
	require.Len(t, m.FrameTable.Frames, 1)
	assert.Equal(t, int32(4096), m.FrameTable.Frames[0].U)
	assert.Equal(t, int32(3500), m.FrameTable.Frames[0].C)

	// No BuildFiles set
	assert.Nil(t, got.BuildFiles)
}

// TestSerializeDeserialize_V4_CompressionNone_EmptyFrames verifies that a
// FrameTable with CompressionNone and zero frames does not corrupt the stream.
// Before the fix, the serializer wrote a StartAt offset (16 bytes) but the
// deserializer skipped it because the packed value was 0.
func TestSerializeDeserialize_V4_CompressionNone_EmptyFrames(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	baseID := uuid.New()
	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        8192,
		Generation:  0,
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 0,
			// FrameTable with CompressionNone and no frames — packed value is 0.
			FrameTable: newFT(storage.CompressionNone, storage.FrameOffset{U: 100, C: 50}, nil),
		},
		{
			Offset:             4096,
			Length:             4096,
			BuildId:            baseID,
			BuildStorageOffset: 0,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)

	// Test with Serialize + Deserialize (unified path)
	data, err := Serialize(h)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 2)

	// First mapping: FrameTable was effectively empty, deserializer should treat as nil.
	assert.Nil(t, got.Mapping[0].FrameTable)

	// Second mapping must not be corrupted by stray StartAt bytes.
	assert.Equal(t, uint64(4096), got.Mapping[1].Offset)
	assert.Equal(t, uint64(4096), got.Mapping[1].Length)
	assert.Equal(t, baseID, got.Mapping[1].BuildId)
}

func TestCompressDecompressLZ4_RoundTrip(t *testing.T) {
	t.Parallel()

	// Random data should round-trip through LZ4 compress/decompress.
	data := make([]byte, 4096)
	_, err := rand.Read(data)
	require.NoError(t, err)

	compressed, err := storage.CompressLZ4(data)
	require.NoError(t, err)

	decompressed, err := storage.DecompressLZ4(compressed, make([]byte, storage.MaxCompressedHeaderSize))
	require.NoError(t, err)
	assert.Equal(t, data, decompressed)
}

func TestSerializeDeserialize_V4_ManyFrames(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	const numFrames = 1000
	frames := make([]storage.FrameSize, numFrames)
	for i := range frames {
		frames[i] = storage.FrameSize{U: 4096, C: int32(2000 + i)}
	}

	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        4096 * numFrames,
		Generation:  0,
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096 * numFrames,
			BuildId:            buildID,
			BuildStorageOffset: 0,
			FrameTable:         newFT(storage.CompressionLZ4, storage.FrameOffset{U: 0, C: 0}, frames),
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)

	// Test with Serialize + Deserialize (unified path)
	data, err := Serialize(h)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	require.NotNil(t, got.Mapping[0].FrameTable)
	require.Len(t, got.Mapping[0].FrameTable.Frames, numFrames)

	// Spot-check first and last frame
	assert.Equal(t, int32(4096), got.Mapping[0].FrameTable.Frames[0].U)
	assert.Equal(t, int32(2000), got.Mapping[0].FrameTable.Frames[0].C)
	assert.Equal(t, int32(4096), got.Mapping[0].FrameTable.Frames[numFrames-1].U)
	assert.Equal(t, int32(2000+numFrames-1), got.Mapping[0].FrameTable.Frames[numFrames-1].C)
}

func TestSerialize_V3_RoundTrip(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	metadata := &Metadata{
		Version:     3,
		BlockSize:   4096,
		Size:        4096,
		Generation:  0,
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	mappings := []*BuildMap{
		{
			Offset:  0,
			Length:  4096,
			BuildId: buildID,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)

	// V3: Serialize should return raw bytes identical to serialize
	unified, err := Serialize(h)
	require.NoError(t, err)

	raw, err := serialize(metadata, nil, mappings)
	require.NoError(t, err)

	assert.Equal(t, raw, unified, "V3 Serialize should produce identical bytes to serialize")

	// Deserialize should handle V3 raw bytes
	got, err := Deserialize(unified)
	require.NoError(t, err)
	assert.Equal(t, metadata, got.Metadata)
}

func TestDeserialize_TooShort(t *testing.T) {
	t.Parallel()

	_, err := Deserialize([]byte{0x01, 0x02})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "header too short")
}

func TestSerializeDeserialize_V4_EmptyBuildFiles(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        4096,
		Generation:  0,
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	mappings := []*BuildMap{
		{
			Offset:  0,
			Length:  4096,
			BuildId: buildID,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	// No BuildFiles set (nil map)

	data, err := Serialize(h)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	assert.Nil(t, got.BuildFiles) // numBuilds=0 → nil
}
