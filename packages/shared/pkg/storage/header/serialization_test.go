package header

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

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

	mappings := []BuildMap{
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

	data, err := serializeV3(metadata, mappings)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	require.Equal(t, metadata, got.Metadata)
	require.Len(t, got.Mapping, 2)
	require.Equal(t, uint64(0), got.Mapping[0].Offset)
	require.Equal(t, uint64(4096), got.Mapping[0].Length)
	require.Equal(t, buildID, got.Mapping[0].BuildId)
	require.Equal(t, uint64(0), got.Mapping[0].BuildStorageOffset)

	require.Equal(t, uint64(4096), got.Mapping[1].Offset)
	require.Equal(t, uint64(4096), got.Mapping[1].Length)
	require.Equal(t, baseID, got.Mapping[1].BuildId)
	require.Equal(t, uint64(123), got.Mapping[1].BuildStorageOffset)

	// V3 headers have no Builds
	require.Nil(t, got.Builds)
}

func TestDeserialize_TruncatedMetadata(t *testing.T) {
	t.Parallel()

	_, err := DeserializeBytes([]byte{0x01, 0x02, 0x03})
	require.Error(t, err)
	require.Contains(t, err.Error(), "header too short")
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

	data, err := serializeV3(metadata, nil)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	// NewHeader creates a default mapping when none provided
	require.Len(t, got.Mapping, 1)
	require.Equal(t, uint64(0), got.Mapping[0].Offset)
	require.Equal(t, metadata.Size, got.Mapping[0].Length)
	require.Equal(t, metadata.BuildId, got.Mapping[0].BuildId)
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

	data, err := serializeV3(metadata, nil)
	require.NoError(t, err)

	_, err = DeserializeBytes(data)
	require.Error(t, err)
	require.Contains(t, err.Error(), "block size cannot be zero")
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

	mappings := []BuildMap{
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
			BuildStorageOffset: 0,
		},
	}

	checksum := sha256.Sum256([]byte("test-data"))

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	h.Builds = map[uuid.UUID]BuildData{
		buildID: {
			Size: 12345, Checksum: checksum,
			FrameData: storage.NewFrameTable(storage.CompressionLZ4, []storage.FrameSize{
				{U: 2048, C: 1024},
				{U: 2048, C: 900},
			}),
		},
		baseID: {Size: 67890},
	}

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	require.Equal(t, uint64(4), got.Metadata.Version)
	require.Len(t, got.Mapping, 2)
	require.Equal(t, buildID, got.Mapping[0].BuildId)
	require.Equal(t, baseID, got.Mapping[1].BuildId)

	// Builds round-trip
	require.Len(t, got.Builds, 2)
	require.Equal(t, int64(12345), got.Builds[buildID].Size)
	require.Equal(t, checksum, got.Builds[buildID].Checksum)
	require.Equal(t, int64(67890), got.Builds[baseID].Size)

	// Frame data round-trip
	fd := got.Builds[buildID].FrameData
	require.NotNil(t, fd)
	require.Equal(t, storage.CompressionLZ4, fd.CompressionType())
	require.Equal(t, 2, fd.NumFrames())

	r, err := fd.LocateCompressed(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 1024, r.Length)

	r, err = fd.LocateCompressed(2048)
	require.NoError(t, err)
	require.Equal(t, int64(1024), r.Offset)
	require.Equal(t, 900, r.Length)

	// baseID has no frames
	require.Nil(t, got.Builds[baseID].FrameData)
}

func TestSerializeDeserialize_V4_Zstd(t *testing.T) {
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

	mappings := []BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 8192,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	// 3 frames; only the third [8192, 12288) overlaps the mapping.
	h.Builds = map[uuid.UUID]BuildData{
		buildID: {
			FrameData: storage.NewFrameTable(storage.CompressionZstd, []storage.FrameSize{
				{U: 4096, C: 2000},
				{U: 4096, C: 3000},
				{U: 4096, C: 3500},
			}),
		},
	}

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	require.Equal(t, uint64(8192), got.Mapping[0].BuildStorageOffset)

	require.Len(t, got.Builds, 1)
	fd := got.Builds[buildID].FrameData
	require.NotNil(t, fd)
	require.Equal(t, storage.CompressionZstd, fd.CompressionType())
	require.Equal(t, 1, fd.NumFrames())

	r, err := fd.LocateCompressed(8192)
	require.NoError(t, err)
	require.Equal(t, int64(2000+3000), r.Offset)
	require.Equal(t, 3500, r.Length)
}

func TestSerializeDeserialize_V4_NoFrames(t *testing.T) {
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

	mappings := []BuildMap{
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
			BuildStorageOffset: 0,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 2)
	require.Nil(t, got.Builds)
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

	mappings := []BuildMap{
		{
			Offset:             0,
			Length:             4096 * numFrames,
			BuildId:            buildID,
			BuildStorageOffset: 0,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	h.Builds = map[uuid.UUID]BuildData{
		buildID: {FrameData: storage.NewFrameTable(storage.CompressionLZ4, frames)},
	}

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	require.NotNil(t, got.Builds)
	fd := got.Builds[buildID].FrameData
	require.NotNil(t, fd)
	require.Equal(t, numFrames, fd.NumFrames())

	r, err := fd.LocateCompressed(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 2000, r.Length)

	r, err = fd.LocateCompressed(int64(4096 * (numFrames - 1)))
	require.NoError(t, err)
	require.Equal(t, 2000+numFrames-1, r.Length)
}

func TestSerializeDeserialize_V4_NoBuilds(t *testing.T) {
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

	mappings := []BuildMap{
		{
			Offset:  0,
			Length:  4096,
			BuildId: buildID,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	// No Builds set (nil map)

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	require.Len(t, got.Mapping, 1)
	require.Nil(t, got.Builds)
}

func TestSerializeDeserialize_V4_MultiBuild_LocateCompressed(t *testing.T) {
	t.Parallel()

	buildA := uuid.New()
	buildB := uuid.New()

	// Build A: 3 frames, each 4096 uncompressed.
	//   Frame 0: U=[0,4096)   C=[0,1000)
	//   Frame 1: U=[4096,8192) C=[1000,2800)
	//   Frame 2: U=[8192,12288) C=[2800,5100)
	ftA := storage.NewFrameTable(storage.CompressionZstd, []storage.FrameSize{
		{U: 4096, C: 1000},
		{U: 4096, C: 1800},
		{U: 4096, C: 2300},
	})

	// Build B: 2 frames, each 4096 uncompressed.
	//   Frame 0: U=[0,4096)   C=[0,500)
	//   Frame 1: U=[4096,8192) C=[500,1700)
	ftB := storage.NewFrameTable(storage.CompressionLZ4, []storage.FrameSize{
		{U: 4096, C: 500},
		{U: 4096, C: 1200},
	})

	// Virtual layout (20480 bytes total):
	//   [0,4096)     → buildA offset 0
	//   [4096,12288) → buildB offset 0 (8192 bytes = both frames of B)
	//   [12288,20480)→ buildA offset 4096 (8192 bytes = frames 1..2 of A)
	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        20480,
		Generation:  2,
		BuildId:     buildA,
		BaseBuildId: buildA,
	}

	mappings := []BuildMap{
		{Offset: 0, Length: 4096, BuildId: buildA, BuildStorageOffset: 0},
		{Offset: 4096, Length: 8192, BuildId: buildB, BuildStorageOffset: 0},
		{Offset: 12288, Length: 8192, BuildId: buildA, BuildStorageOffset: 4096},
	}

	checksumA := sha256.Sum256([]byte("build-a"))
	checksumB := sha256.Sum256([]byte("build-b"))

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	h.Builds = map[uuid.UUID]BuildData{
		buildA: {Size: 12288, Checksum: checksumA, FrameData: ftA},
		buildB: {Size: 8192, Checksum: checksumB, FrameData: ftB},
	}

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	require.Equal(t, uint64(4), got.Metadata.Version)
	require.Len(t, got.Mapping, 3)
	require.Len(t, got.Builds, 2)

	// Verify checksums round-trip.
	require.Equal(t, checksumA, got.Builds[buildA].Checksum)
	require.Equal(t, checksumB, got.Builds[buildB].Checksum)

	// --- Build A frame lookups via GetBuildFrameData ---
	fdA := got.GetBuildFrameData(buildA)
	require.NotNil(t, fdA)
	require.Equal(t, storage.CompressionZstd, fdA.CompressionType())
	// All 3 frames should survive trimming: frame 0 referenced by mapping 0,
	// frames 1-2 referenced by mapping 2.
	require.Equal(t, 3, fdA.NumFrames())

	// Frame 0 of A: U=0, C offset=0, C length=1000.
	r, err := fdA.LocateCompressed(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 1000, r.Length)

	// Frame 1 of A: U=4096, C offset=1000, C length=1800.
	r, err = fdA.LocateCompressed(4096)
	require.NoError(t, err)
	require.Equal(t, int64(1000), r.Offset)
	require.Equal(t, 1800, r.Length)

	// Frame 2 of A: U=8192, C offset=2800, C length=2300.
	r, err = fdA.LocateCompressed(8192)
	require.NoError(t, err)
	require.Equal(t, int64(2800), r.Offset)
	require.Equal(t, 2300, r.Length)

	// --- Build B frame lookups via GetBuildFrameData ---
	fdB := got.GetBuildFrameData(buildB)
	require.NotNil(t, fdB)
	require.Equal(t, storage.CompressionLZ4, fdB.CompressionType())
	require.Equal(t, 2, fdB.NumFrames())

	// Frame 0 of B: U=0, C offset=0, C length=500.
	r, err = fdB.LocateCompressed(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 500, r.Length)

	// Frame 1 of B: U=4096, C offset=500, C length=1200.
	r, err = fdB.LocateCompressed(4096)
	require.NoError(t, err)
	require.Equal(t, int64(500), r.Offset)
	require.Equal(t, 1200, r.Length)

	// Beyond end of B's frames.
	_, err = fdB.LocateCompressed(8192)
	require.Error(t, err)
}

func TestSerializeDeserialize_V4_TrimmedOffsets_Error(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()

	// 4 frames, each 4096 uncompressed.
	ft := storage.NewFrameTable(storage.CompressionZstd, []storage.FrameSize{
		{U: 4096, C: 2000},
		{U: 4096, C: 3000},
		{U: 4096, C: 3500},
		{U: 4096, C: 1800},
	})

	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        4096,
		Generation:  0,
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	// Mapping references only frame 2 (BuildStorageOffset=8192, Length=4096).
	// Frames 0, 1, and 3 should be trimmed away.
	mappings := []BuildMap{
		{
			Offset:             0,
			Length:             4096,
			BuildId:            buildID,
			BuildStorageOffset: 8192,
		},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	h.Builds = map[uuid.UUID]BuildData{
		buildID: {FrameData: ft},
	}

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	fd := got.Builds[buildID].FrameData
	require.NotNil(t, fd)
	require.Equal(t, 1, fd.NumFrames(), "only frame 2 should survive trimming")

	// The surviving frame covers U=[8192,12288). Lookup should succeed.
	r, err := fd.LocateCompressed(8192)
	require.NoError(t, err)
	require.Equal(t, int64(5000), r.Offset)
	require.Equal(t, 3500, r.Length)

	// Trimmed offsets should return errors.
	_, err = fd.LocateCompressed(0)
	require.Error(t, err, "frame at offset 0 was trimmed, should error")

	_, err = fd.LocateCompressed(4096)
	require.Error(t, err, "frame at offset 4096 was trimmed, should error")

	_, err = fd.LocateCompressed(12288)
	require.Error(t, err, "frame at offset 12288 was trimmed, should error")

	// Completely beyond original range.
	_, err = fd.LocateCompressed(16384)
	require.Error(t, err, "offset beyond all frames should error")
}

func TestFrameTable_LocateCompressed(t *testing.T) {
	t.Parallel()

	fd := storage.NewFrameTable(storage.CompressionZstd, []storage.FrameSize{
		{U: 2048, C: 1024},
		{U: 2048, C: 900},
		{U: 4096, C: 3500},
	})

	// Frame 0: U=[0,2048), C=[0,1024)
	r, err := fd.LocateCompressed(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 1024, r.Length)

	r, err = fd.LocateCompressed(2047)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 1024, r.Length)

	// Frame 1: U=[2048,4096), C=[1024,1924)
	r, err = fd.LocateCompressed(2048)
	require.NoError(t, err)
	require.Equal(t, int64(1024), r.Offset)
	require.Equal(t, 900, r.Length)

	// Frame 2: U=[4096,8192), C=[1924,5424)
	r, err = fd.LocateCompressed(4096)
	require.NoError(t, err)
	require.Equal(t, int64(1924), r.Offset)
	require.Equal(t, 3500, r.Length)

	// Beyond end
	_, err = fd.LocateCompressed(8192)
	require.Error(t, err)
}

func TestFrameTable_LocateUncompressed(t *testing.T) {
	t.Parallel()

	fd := storage.NewFrameTable(storage.CompressionZstd, []storage.FrameSize{
		{U: 2048, C: 1024},
		{U: 4096, C: 3500},
	})

	// Frame 0: U=[0,2048)
	r, err := fd.LocateUncompressed(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 2048, r.Length)

	// Frame 1: U=[2048,6144)
	r, err = fd.LocateUncompressed(2048)
	require.NoError(t, err)
	require.Equal(t, int64(2048), r.Offset)
	require.Equal(t, 4096, r.Length)

	// Beyond end
	_, err = fd.LocateUncompressed(6144)
	require.Error(t, err)
}

func TestSerializeDeserialize_V4_SparseTrimming(t *testing.T) {
	t.Parallel()

	buildID := uuid.New()
	otherID := uuid.New()

	ft := storage.NewFrameTable(storage.CompressionLZ4, []storage.FrameSize{
		{U: 4096, C: 2000},
		{U: 4096, C: 3000},
		{U: 4096, C: 2500},
		{U: 4096, C: 1800},
	})

	metadata := &Metadata{
		Version:     4,
		BlockSize:   4096,
		Size:        4096 * 4,
		Generation:  0,
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	// Mapping only references frames 0 and 3 (gap at 1,2 due to otherID).
	mappings := []BuildMap{
		{Offset: 0, Length: 4096, BuildId: buildID, BuildStorageOffset: 0},
		{Offset: 4096, Length: 8192, BuildId: otherID, BuildStorageOffset: 0},
		{Offset: 12288, Length: 4096, BuildId: buildID, BuildStorageOffset: 12288},
	}

	h, err := NewHeader(metadata, mappings)
	require.NoError(t, err)
	h.Builds = map[uuid.UUID]BuildData{
		buildID: {FrameData: ft, Size: 16384},
		otherID: {Size: 8192},
	}

	data, err := SerializeHeader(h)
	require.NoError(t, err)

	got, err := DeserializeBytes(data)
	require.NoError(t, err)

	gotFT := got.Builds[buildID].FrameData
	require.NotNil(t, gotFT)
	require.Equal(t, 2, gotFT.NumFrames())

	// Frame 0
	r, err := gotFT.LocateCompressed(0)
	require.NoError(t, err)
	require.Equal(t, int64(0), r.Offset)
	require.Equal(t, 2000, r.Length)

	// Frame 3
	r, err = gotFT.LocateCompressed(12288)
	require.NoError(t, err)
	require.Equal(t, int64(2000+3000+2500), r.Offset)
	require.Equal(t, 1800, r.Length)

	// Gap
	_, err = gotFT.LocateCompressed(4096)
	require.Error(t, err)
}
