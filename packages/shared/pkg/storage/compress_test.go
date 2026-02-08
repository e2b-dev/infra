package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreFile_Compressed(t *testing.T) {
	t.Parallel()

	const (
		chunkSize = 2048
		frameSize = 1024
		partSize  = 2048

		chunksInData   = 100
		targetNumParts = 3
	)

	var origData []byte
	seeded := rand.New(rand.NewSource(42))

	// t.Run("Generate test data", func(t *testing.T) {
	for range chunksInData {
		// read in some random bytes
		chunk := make([]byte, 0, chunkSize*2)
		for len(chunk) < chunkSize {
			n := seeded.Intn(64) + 1
			b := seeded.Intn(256)
			for range n {
				chunk = append(chunk, byte(b))
			}
		}
		chunk = chunk[:chunkSize]
		origData = append(origData, chunk...)
	}
	// })

	var data []byte
	var frameTable *FrameTable
	var err error
	receivedParts := make(map[string][]byte)
	receivedData := make([]byte, 0)
	t.Log("Frame compressed parallel upload")

	mu := &sync.Mutex{}
	var uploadID string
	var initiateC int
	var completeC int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.RawQuery == uploadsPath:
			// Initiate upload
			uploadID = "test-upload-id-123"
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: uploadID,
			}
			xmlData, _ := xml.Marshal(response)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)
			initiateC++

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			partNum := r.URL.Query().Get("partNumber")
			assert.NotEmpty(t, partNum)

			// Upload part
			body, _ := io.ReadAll(r.Body)
			receivedParts[partNum] = body

			// Simulate variable network/upload delay, 50-1000ms
			randomDelay := seeded.Intn(50)
			time.Sleep(time.Duration(randomDelay) * time.Millisecond)

			w.Header().Set("ETag", fmt.Sprintf(`"etag%s"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			// Complete upload
			completeC++
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestMultipartUploader(t, handler)

	opts := FramedUploadOptions{
		CompressionType: CompressionZstd,
		Level:           int(zstd.SpeedBestCompression), // Use fixed level for deterministic test
		ChunkSize:       chunkSize,
		TargetFrameSize: frameSize,
	}
	e := newFrameEncoder(&opts, uploader, partSize, 4)

	frameTable, err = e.uploadFramed(t.Context(), bytes.NewReader(origData))
	require.NoError(t, err)
	require.Equal(t, 1, initiateC)
	require.Equal(t, 1, completeC)
	require.Len(t, receivedParts, 7, "should have been at least 4 parts uploaded")
	require.Len(t, frameTable.Frames, 13)

	totalUncompressed := 0
	for _, frame := range frameTable.Frames {
		totalUncompressed += int(frame.U)
		require.LessOrEqual(t, int(frame.C), int(frame.U),
			"expect that all frames get somewhat compressed due to the nature of the data")
		require.Equal(t, 0, int(frame.U)%chunkSize,
			"expect each frame's uncompressed size to be multiple of chunk size")
	}
	require.Equal(t, len(origData), totalUncompressed,
		"expect total uncompressed size in frame info to match original data length")

	// Verify uploaded parts
	for i := range len(receivedParts) {
		partNum := i + 1
		partData, ok := receivedParts[fmt.Sprintf("%d", partNum)]
		require.True(t, ok, "missing part %d", partNum)
		receivedData = append(receivedData, partData...)

		// each part is decompressable
		zr, err := zstd.NewReader(nil)
		require.NoError(t, err)
		uncompressed, err := zr.DecodeAll(partData, nil)
		require.NoError(t, err)
		zr.Close()
		data = append(data, uncompressed...)
	}

	// Verify full data
	require.Equal(t, origData, data, "expected uploaded data to match original data")

	t.Log("Verify downloading slices")
	fake := &Storage{
		Backend: &Backend{
			RangeGetter: &fakeRanger{data: receivedData},
		},
	}

	for range 10 {
		offset := int64(seeded.Intn(len(origData) - 1))

		t.Logf("requesting frame for offset %#x\n", offset)

		// Get frame info to know the uncompressed size
		frameStart, frameSize, err := frameTable.FrameFor(offset)
		require.NoError(t, err)

		// Buffer must be large enough for UNCOMPRESSED frame data.
		// GetFrame internally uses len(buf) to determine the range to read,
		// so we pass a buffer exactly the size of the uncompressed frame.
		// The offset is adjusted to the start of the frame for proper reading.
		buf := make([]byte, frameSize.U)
		rr, err := fake.GetFrame(t.Context(), "test-path", frameStart.U, frameTable, true, buf)
		require.NoError(t, err)
		require.Equal(t, int(frameStart.C), int(rr.Start))
		require.Equal(t, int(frameSize.U), rr.Length, "should read full uncompressed frame")

		// Verify the specific byte at the original offset is correct
		offsetInFrame := int(offset - frameStart.U)
		require.Equal(t, origData[offset], buf[offsetInFrame],
			"byte at offset %d should match original data", offset)
	}
}

type fakeRanger struct {
	data []byte
}

func (r *fakeRanger) RangeGet(_ context.Context, _ string, offset int64, length int) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.data[offset : offset+int64(length)])), nil
}

func TestCompressedInfo_Subset(t *testing.T) {
	t.Parallel()

	// Create a FrameTable with frames of known sizes
	// Frame 0: U=1000, C=500 (100-1100)
	// Frame 1: U=2000, C=1000 (1100-3100)
	// Frame 2: U=1500, C=750 (3100-4600)
	// Frame 3: U=3000, C=1500 (4600-7600)
	// Total uncompressed: 7500 bytes
	ft := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 100, C: 50},
		Frames: []FrameSize{
			{U: 1000, C: 500},
			{U: 2000, C: 1000},
			{U: 1500, C: 750},
			{U: 3000, C: 1500},
		},
	}

	t.Run("subset at the beginning", func(t *testing.T) {
		t.Parallel()

		// Request range [0, 500) - should include only frame 0
		subset, err := ft.Subset(Range{Start: 100, Length: 500})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Equal(t, CompressionZstd, subset.CompressionType)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, int64(100), subset.StartAt.U)
		require.Equal(t, int64(50), subset.StartAt.C)
		require.Equal(t, FrameSize{U: 1000, C: 500}, subset.Frames[0])
	})

	t.Run("subset exactly one frame", func(t *testing.T) {
		t.Parallel()

		// Request range [3100, 4600) - should include frame 2
		subset, err := ft.Subset(Range{Start: 3100, Length: 1500})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, int64(3100), subset.StartAt.U)
		require.Equal(t, int64(1550), subset.StartAt.C)
		require.Equal(t, FrameSize{U: 1500, C: 750}, subset.Frames[0])
	})

	t.Run("subset spanning multiple frames", func(t *testing.T) {
		t.Parallel()

		// Request range [500, 3500) - should include frames 0, 1, 2
		subset, err := ft.Subset(Range{Start: 500, Length: 3000})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 3)
		require.Equal(t, FrameSize{U: 1000, C: 500}, subset.Frames[0])
		require.Equal(t, FrameSize{U: 2000, C: 1000}, subset.Frames[1])
		require.Equal(t, FrameSize{U: 1500, C: 750}, subset.Frames[2])
	})

	t.Run("subset at the end", func(t *testing.T) {
		t.Parallel()

		// Request range [6000, 1500) - should include only frame 3
		subset, err := ft.Subset(Range{Start: 6000, Length: 1500})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, int64(4600), subset.StartAt.U)
		require.Equal(t, int64(2300), subset.StartAt.C)
		require.Equal(t, FrameSize{U: 3000, C: 1500}, subset.Frames[0])
	})

	t.Run("subset entire range", func(t *testing.T) {
		t.Parallel()

		// Request range [0, 7500) - should include all frames
		subset, err := ft.Subset(Range{Start: 100, Length: 7500})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 4)
		require.Equal(t, int64(100), subset.StartAt.U)
		require.Equal(t, int64(50), subset.StartAt.C)
		require.Equal(t, ft.Frames, subset.Frames)
	})

	t.Run("subset with frame boundaries aligned", func(t *testing.T) {
		t.Parallel()

		// Request range [1100, 3000) - exactly frames 1 and 2
		subset, err := ft.Subset(Range{Start: 1100, Length: 3000})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 2)
		require.Equal(t, int64(1100), subset.StartAt.U)
		require.Equal(t, int64(550), subset.StartAt.C)
		require.Equal(t, FrameSize{U: 2000, C: 1000}, subset.Frames[0])
		require.Equal(t, FrameSize{U: 1500, C: 750}, subset.Frames[1])
	})

	t.Run("subset starting in middle of frame", func(t *testing.T) {
		t.Parallel()

		// Request range [1500, 2000) - starts in frame 1, needs entire frames 1 and 2
		subset, err := ft.Subset(Range{Start: 1500, Length: 2000})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 2)
		require.Equal(t, FrameSize{U: 2000, C: 1000}, subset.Frames[0])
		require.Equal(t, FrameSize{U: 1500, C: 750}, subset.Frames[1])
	})

	t.Run("subset ending in middle of frame", func(t *testing.T) {
		t.Parallel()

		// Request range [3200, 500) - ends in frame 2, includes frame 2
		subset, err := ft.Subset(Range{Start: 3100, Length: 500})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, int64(3100), subset.StartAt.U)
		require.Equal(t, int64(1550), subset.StartAt.C)
		require.Equal(t, FrameSize{U: 1500, C: 750}, subset.Frames[0])
	})

	t.Run("subset beyond total size stops at end", func(t *testing.T) {
		t.Parallel()

		// Request range [7000, 1000) - end exceeds total size, stops at frame 3
		subset, err := ft.Subset(Range{Start: 7000, Length: 1000})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, int64(4600), subset.StartAt.U)
		require.Equal(t, int64(2300), subset.StartAt.C)
		require.Equal(t, FrameSize{U: 3000, C: 1500}, subset.Frames[0])
	})

	t.Run("subset starting beyond total size returns empty", func(t *testing.T) {
		t.Parallel()

		// Request range [8000, 100) - start beyond total size, no frames included
		subset, err := ft.Subset(Range{Start: 8000, Length: 100})
		require.Contains(t, err.Error(), "requested range is beyond the end of the frame table")
		require.Nil(t, subset)
	})

	t.Run("subset with zero length is nil", func(t *testing.T) {
		t.Parallel()

		// Request range [1000, 0) - zero length, should return empty subset
		subset, err := ft.Subset(Range{Start: 1000, Length: 0})
		require.NoError(t, err)
		require.Nil(t, subset)
	})

	t.Run("subset single byte at start", func(t *testing.T) {
		t.Parallel()

		// Request range [0, 1) - single byte, needs entire first frame
		subset, err := ft.Subset(Range{Start: 100, Length: 1})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, FrameSize{U: 1000, C: 500}, subset.Frames[0])
	})

	t.Run("subset single byte in middle", func(t *testing.T) {
		t.Parallel()

		// Request range [2500, 1) - single byte in frame 1, needs entire frame
		subset, err := ft.Subset(Range{Start: 2500, Length: 1})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, FrameSize{U: 2000, C: 1000}, subset.Frames[0])
	})

	t.Run("subset single byte at end", func(t *testing.T) {
		t.Parallel()

		// Request range [7499, 1) - last byte, needs entire last frame
		subset, err := ft.Subset(Range{Start: 7499, Length: 1})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, FrameSize{U: 3000, C: 1500}, subset.Frames[0])
	})

	t.Run("subset preserves compression type", func(t *testing.T) {
		t.Parallel()

		// Verify compression type is preserved
		subset, err := ft.Subset(Range{Start: 100, Length: 1000})
		require.NoError(t, err)
		require.Equal(t, CompressionZstd, subset.CompressionType)
	})

	t.Run("subset with large frames", func(t *testing.T) {
		t.Parallel()

		// Create FrameTable with larger frames
		largeCi := &FrameTable{
			CompressionType: CompressionZstd,
			Frames: []FrameSize{
				{U: 1000000, C: 500000},  // 1MB uncompressed
				{U: 2000000, C: 1000000}, // 2MB uncompressed
				{U: 1000000, C: 500000},  // 1MB uncompressed
			},
		}

		// Request middle portion
		subset, err := largeCi.Subset(Range{Start: 500000, Length: 2000000})
		require.NoError(t, err)
		require.Len(t, subset.Frames, 2)
		require.Equal(t, FrameSize{U: 1000000, C: 500000}, subset.Frames[0])
		require.Equal(t, FrameSize{U: 2000000, C: 1000000}, subset.Frames[1])
	})

	t.Run("subset from empty FrameTable returns empty", func(t *testing.T) {
		t.Parallel()

		emptyCi := &FrameTable{
			CompressionType: CompressionZstd,
			Frames:          []FrameSize{},
		}

		// Request returns empty subset
		subset, err := emptyCi.Subset(Range{Start: 0, Length: 100})
		require.Contains(t, err.Error(), "requested range is beyond the end of the frame table")
		require.Nil(t, subset)
	})

	t.Run("subset beyond end stops gracefully", func(t *testing.T) {
		t.Parallel()

		totalSize := int(ft.TotalUncompressedSize())
		require.Equal(t, 7500, totalSize)

		// Requesting exactly the total size should work
		subset, err := ft.Subset(Range{Start: 100, Length: totalSize})
		require.NoError(t, err)
		require.Len(t, subset.Frames, 4)

		// Requesting one byte more stops at the end
		subset, err = ft.Subset(Range{Start: 100, Length: totalSize + 1})
		require.NoError(t, err)
		require.Len(t, subset.Frames, 4)
	})

	t.Run("Subset of nil is nil", func(t *testing.T) {
		t.Parallel()

		var nilCi *FrameTable
		subset, err := nilCi.Subset(Range{Start: 0, Length: 100})
		require.NoError(t, err)
		require.Nil(t, subset)
	})

	t.Run("Subset starts before the start of the frameTable", func(t *testing.T) {
		t.Parallel()

		_, err := ft.Subset(Range{Start: 50, Length: 100})
		require.Contains(t, err.Error(), "requested range starts before the beginning of the frame table")
	})
}

// TestGetFrame_FullFrameDecompression tests reading an entire frame
func TestGetFrame_FullFrameDecompression(t *testing.T) {
	t.Parallel()

	// Create compressible test data
	uncompressedSize := 8192
	origData := make([]byte, uncompressedSize)
	for i := range origData {
		origData[i] = byte(i % 256)
	}

	// Compress the data
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	require.NoError(t, err)
	compressedData := enc.EncodeAll(origData, nil)
	enc.Close()

	compressedSize := len(compressedData)

	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames: []FrameSize{
			{U: int32(uncompressedSize), C: int32(compressedSize)},
		},
	}

	fake := &Storage{
		Backend: &Backend{
			RangeGetter: &fakeRanger{data: compressedData},
		},
	}

	// Request the full frame (uncompressed size)
	buf := make([]byte, uncompressedSize)
	rr, err := fake.GetFrame(t.Context(), "test-path", 0, frameTable, true, buf)

	// This should work because we're requesting the full uncompressed size
	require.NoError(t, err)
	require.Equal(t, uncompressedSize, rr.Length)
	require.Equal(t, origData, buf, "decompressed data should match original")
}

// TestStoreFile_Compressed_FS tests compression using the local filesystem backend
func TestStoreFile_Compressed_FS(t *testing.T) {
	t.Parallel()
	if !EnableGCSCompression {
		t.Skip("skipping compression test when EnableGCSCompression is false")
	}

	tempDir := t.TempDir()
	storage := &Storage{Backend: NewFS(tempDir)}

	const dataSize = 100 * 1024 // 100KB

	// Create test data with repetitive pattern (compresses well)
	origData := make([]byte, dataSize)
	for i := range origData {
		origData[i] = byte(i % 64)
	}

	// Write test file
	inputFile := tempDir + "/input.dat"
	err := os.WriteFile(inputFile, origData, 0o644)
	require.NoError(t, err)

	// Store with compression
	frameTable, err := storage.StoreFile(t.Context(), inputFile, "output.compressed", DefaultCompressionOptions)
	require.NoError(t, err)
	require.NotNil(t, frameTable)

	// Verify frame table
	var totalU int64
	for _, f := range frameTable.Frames {
		totalU += int64(f.U)
		require.Positive(t, f.U, "frame should have uncompressed data")
		require.Positive(t, f.C, "frame should have compressed data")
	}
	require.Equal(t, int64(dataSize), totalU, "total uncompressed size should match")

	// Read back the compressed file
	compressedData, err := os.ReadFile(tempDir + "/output.compressed")
	require.NoError(t, err)
	require.Less(t, len(compressedData), dataSize, "compressed data should be smaller")

	// Decompress and verify
	dec, err := zstd.NewReader(nil)
	require.NoError(t, err)
	decompressed, err := dec.DecodeAll(compressedData, nil)
	require.NoError(t, err)
	dec.Close()

	require.Equal(t, origData, decompressed, "decompressed data should match original")

	t.Logf("Original: %d bytes, Compressed: %d bytes, Frames: %d",
		dataSize, len(compressedData), len(frameTable.Frames))
}

// TestStoreFile_Compressed_FS_RoundTrip tests full round-trip: store compressed, then read back
func TestStoreFile_Compressed_FS_RoundTrip(t *testing.T) {
	t.Parallel()
	if !EnableGCSCompression {
		t.Skip("skipping compression test when EnableGCSCompression is false")
	}

	tempDir := t.TempDir()
	storage := &Storage{Backend: NewFS(tempDir)}

	const dataSize = 50 * 1024 // 50KB

	// Create test data
	origData := make([]byte, dataSize)
	r := rand.New(rand.NewSource(42))
	// Mix of random and repetitive data
	for i := range origData {
		if i%100 < 70 {
			origData[i] = byte(i % 32) // repetitive
		} else {
			origData[i] = byte(r.Intn(256)) // random
		}
	}

	// Write test file
	inputFile := tempDir + "/input.dat"
	err := os.WriteFile(inputFile, origData, 0o644)
	require.NoError(t, err)

	// Store with compression
	frameTable, err := storage.StoreFile(t.Context(), inputFile, "data.zst", DefaultCompressionOptions)
	require.NoError(t, err)
	require.NotNil(t, frameTable)

	// Now test reading back specific ranges using GetFrame
	testOffsets := []int64{0, 1000, 25000, 49000}
	for _, offset := range testOffsets {
		t.Run(fmt.Sprintf("offset_%d", offset), func(t *testing.T) {
			t.Parallel()
			// Get frame info to know the uncompressed size
			frameStart, frameSize, err := frameTable.FrameFor(offset)
			require.NoError(t, err)

			// Create buffer large enough for UNCOMPRESSED data
			frameBuf := make([]byte, frameSize.U)
			rr, err := storage.GetFrame(t.Context(), "data.zst", offset, frameTable, true, frameBuf)
			require.NoError(t, err)
			require.Equal(t, int(frameSize.U), rr.Length, "should read full uncompressed frame")

			t.Logf("Offset %d: frameStart C:%d, frameSize U:%d/C:%d, returned %d bytes",
				offset, frameStart.C, frameSize.U, frameSize.C, rr.Length)

			// Verify the data matches original
			// The frame starts at frameStart.U in uncompressed coordinates
			expectedData := origData[frameStart.U : frameStart.U+int64(frameSize.U)]
			require.Equal(t, expectedData, frameBuf, "decompressed frame should match original data")
		})
	}
}

// TestFrameTable_GetFetchRange_CompressedVsUncompressed verifies the relationship
// between compressed and uncompressed coordinates
func TestFrameTable_GetFetchRange_CompressedVsUncompressed(t *testing.T) {
	t.Parallel()

	// Create a frame table with known sizes
	// Frame 0: U=1000, C=500 at offset U:0, C:0
	// Frame 1: U=2000, C=800 at offset U:1000, C:500
	// Frame 2: U=1500, C=600 at offset U:3000, C:1300
	ft := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames: []FrameSize{
			{U: 1000, C: 500},
			{U: 2000, C: 800},
			{U: 1500, C: 600},
		},
	}

	testCases := []struct {
		name           string
		requestU       Range
		expectedStartC int64
		expectedLenC   int
	}{
		{
			name:           "first frame",
			requestU:       Range{Start: 0, Length: 100},
			expectedStartC: 0,
			expectedLenC:   500,
		},
		{
			name:           "second frame",
			requestU:       Range{Start: 1500, Length: 100},
			expectedStartC: 500,
			expectedLenC:   800,
		},
		{
			name:           "third frame",
			requestU:       Range{Start: 3500, Length: 100},
			expectedStartC: 1300,
			expectedLenC:   600,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fetchRange, err := ft.GetFetchRange(tc.requestU)
			require.NoError(t, err)

			require.Equal(t, tc.expectedStartC, fetchRange.Start,
				"compressed start offset should match")
			require.Equal(t, tc.expectedLenC, fetchRange.Length,
				"compressed length should match frame's compressed size")

			// Note: fetchRange.Length is the COMPRESSED size, not uncompressed
			// This is important for understanding the GetFrame buffer issue
			t.Logf("Request U:%d/%d -> Fetch C:%d/%d",
				tc.requestU.Start, tc.requestU.Length,
				fetchRange.Start, fetchRange.Length)
		})
	}
}

// TestStoreFile_DataIntegrity_FS tests data integrity with various data patterns using FS
func TestStoreFile_DataIntegrity_FS(t *testing.T) {
	t.Parallel()
	if !EnableGCSCompression {
		t.Skip("skipping compression test when EnableGCSCompression is false")
	}

	testCases := []struct {
		name string
		data []byte
	}{
		{
			name: "zeros",
			data: make([]byte, 20*1024), // 20KB of zeros
		},
		{
			name: "sequential",
			data: func() []byte {
				d := make([]byte, 20*1024)
				for i := range d {
					d[i] = byte(i % 256)
				}

				return d
			}(),
		},
		{
			name: "random_seeded",
			data: func() []byte {
				r := rand.New(rand.NewSource(42))
				d := make([]byte, 20*1024)
				r.Read(d)

				return d
			}(),
		},
		{
			name: "repetitive_compressible",
			data: func() []byte {
				d := make([]byte, 20*1024)
				pattern := []byte("ABCDEFGH")
				for i := range d {
					d[i] = pattern[i%len(pattern)]
				}

				return d
			}(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			storage := &Storage{Backend: NewFS(tempDir)}

			// Write test file
			inputFile := tempDir + "/input.dat"
			err := os.WriteFile(inputFile, tc.data, 0o644)
			require.NoError(t, err)

			// Store with compression
			frameTable, err := storage.StoreFile(t.Context(), inputFile, "output.zst", DefaultCompressionOptions)
			require.NoError(t, err)

			// Read compressed file
			compressedData, err := os.ReadFile(tempDir + "/output.zst")
			require.NoError(t, err)

			// Decompress and verify
			dec, err := zstd.NewReader(nil)
			require.NoError(t, err)
			decompressed, err := dec.DecodeAll(compressedData, nil)
			require.NoError(t, err)
			dec.Close()

			require.Equal(t, tc.data, decompressed, "decompressed data should match original")

			// Verify frame table
			var totalU int64
			for _, f := range frameTable.Frames {
				totalU += int64(f.U)
			}
			require.Equal(t, int64(len(tc.data)), totalU)

			t.Logf("Data size: %d, Compressed: %d, Frames: %d",
				len(tc.data), len(compressedData), len(frameTable.Frames))
		})
	}
}
