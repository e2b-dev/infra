package header

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestSerializeDeserialize_V3_DropsFrames(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	baseID := uuid.New()
	metadata := &Metadata{
		Version:     3,
		BlockSize:   4096,
		Size:        4096, // Size matches mapping length
		Generation:  7,
		BuildId:     buildID,
		BaseBuildId: baseID,
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 123,
			FrameTable: &storage.FrameTable{
				CompressionType: storage.CompressionZstd,
				StartAt:         storage.FrameOffset{U: 11, C: 22},
				Frames: []storage.FrameSize{
					{U: 100, C: 50},
				},
			},
		},
	}

	data, err := Serialize(metadata, mappings)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Equal(t, metadata.Version, got.Metadata.Version)
	require.Equal(t, metadata.BlockSize, got.Metadata.BlockSize)
	require.Equal(t, metadata.Size, got.Metadata.Size)
	require.Equal(t, metadata.Generation, got.Metadata.Generation)
	require.Equal(t, metadata.BuildId, got.Metadata.BuildId)
	require.Equal(t, metadata.BaseBuildId, got.Metadata.BaseBuildId)

	require.Len(t, got.Mapping, 1)
	assert.Equal(t, uint64(0), got.Mapping[0].Offset)
	assert.Equal(t, uint64(4096), got.Mapping[0].Length)
	assert.Equal(t, buildID, got.Mapping[0].BuildId)
	assert.Equal(t, uint64(123), got.Mapping[0].BuildStorageOffset)
	assert.Nil(t, got.Mapping[0].FrameTable)
}

func TestSerializeDeserialize_V4_RoundTripFrames(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	baseID := uuid.New()
	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        4096, // Size matches mapping length
		Generation:  2,
		BuildId:     buildID,
		BaseBuildId: baseID,
	}

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: 1024, C: 512},
			{U: 2048, C: 1024},
		},
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 777,
			FrameTable:         frameTable,
		},
	}

	data, err := Serialize(metadata, mappings)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	m := got.Mapping[0]
	assert.Equal(t, mappings[0].Offset, m.Offset)
	assert.Equal(t, mappings[0].Length, m.Length)
	assert.Equal(t, mappings[0].BuildId, m.BuildId)
	assert.Equal(t, mappings[0].BuildStorageOffset, m.BuildStorageOffset)
	require.NotNil(t, m.FrameTable)
	assert.Equal(t, frameTable.CompressionType, m.FrameTable.CompressionType)
	assert.Equal(t, frameTable.StartAt, m.FrameTable.StartAt)
	assert.Equal(t, frameTable.Frames, m.FrameTable.Frames)
}

func TestDeserialize_TruncatedMetadata(t *testing.T) {
	t.Parallel()

	_, err := Deserialize([]byte{0x01, 0x02, 0x03})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read metadata")
}

func TestDeserialize_V4_TruncatedFrames(t *testing.T) {
	t.Parallel()

	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        16384,
		Generation:  1,
		BuildId:     uuid.New(),
		BaseBuildId: uuid.New(),
	}

	mappings := []*BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            uuid.New(),
			BuildStorageOffset: 0,
			FrameTable: &storage.FrameTable{
				CompressionType: storage.CompressionZstd,
				StartAt:         storage.FrameOffset{U: 0, C: 0},
				Frames:          []storage.FrameSize{{U: 1, C: 1}, {U: 2, C: 2}},
			},
		},
	}

	data, err := Serialize(metadata, mappings)
	require.NoError(t, err)
	require.Greater(t, len(data), 8)

	truncated := data[:len(data)-8]
	_, err = Deserialize(truncated)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "compression frame") || strings.Contains(err.Error(), "compression frames starting offset"))
}

func TestSerializeDeserialize_EmptyMappings_Defaults(t *testing.T) {
	t.Parallel()

	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        8192,
		Generation:  0,
		BuildId:     uuid.New(),
		BaseBuildId: uuid.New(),
	}

	data, err := Serialize(metadata, nil)
	require.NoError(t, err)

	got, err := Deserialize(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	assert.Equal(t, uint64(0), got.Mapping[0].Offset)
	assert.Equal(t, metadata.Size, got.Mapping[0].Length)
	assert.Equal(t, metadata.BuildId, got.Mapping[0].BuildId)
}

func TestDeserialize_BlockSizeZero(t *testing.T) {
	t.Parallel()

	metadata := &Metadata{
		Version:     4,
		BlockSize:   0,
		Size:        4096,
		Generation:  0,
		BuildId:     uuid.New(),
		BaseBuildId: uuid.New(),
	}

	data, err := Serialize(metadata, nil)
	require.NoError(t, err)

	_, err = Deserialize(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "block size cannot be zero")
}
