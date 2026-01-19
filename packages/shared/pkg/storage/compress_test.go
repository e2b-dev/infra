package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultipartCompressUploadFile_Success(t *testing.T) {
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
	t.Run("Frame compressed parallel upload", func(t *testing.T) {
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
			Level:           int(defaultZstdCompressionLevel),
			ChunkSize:       chunkSize,
			TargetFrameSize: frameSize,
		}
		e := newFrameEncoder(&opts, uploader, 4)
		e.targetPartSize = partSize

		frameTable, err = e.uploadFramed(t.Context(), "test-path", bytes.NewReader(origData))
		require.NoError(t, err)
		require.Equal(t, 1, initiateC)
		require.Equal(t, 1, completeC)
		require.Equal(t, 7, len(receivedParts), "should have been at least 4 parts uploaded")
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
	})

	t.Run("Verify downloading slices", func(t *testing.T) {
		fake := &Storage{
			Provider: &Provider{
				RangeGetter: &fakeRanger{data: receivedData},
			},
		}

		for range 10 {
			r := Range{
				Start:  int64(seeded.Intn(len(origData) - 1)),
				Length: 1,
			}

			t.Logf("requesting frames for range %v\n", r)

			fetchRange, err := frameTable.GetFetchRange(r)
			require.NoError(t, err)

			buf := make([]byte, r.Length, fetchRange.Length)
			rr, err := fake.GetFrame(t.Context(), "test-path", r.Start, frameTable, true, buf)
			require.NoError(t, err)
			require.Equal(t, int(fetchRange.Start), int(rr.Start))
			require.Equal(t, int(fetchRange.Length), int(rr.Length))
		}
	})
}

type fakeRanger struct {
	data []byte
}

func (r *fakeRanger) RangeGet(_ context.Context, path string, offset int64, length int) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.data[offset : offset+int64(length)])), nil
}

func TestCompressedInfo_Subset(t *testing.T) {
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
		// Request range [1500, 2000) - starts in frame 1, needs entire frames 1 and 2
		subset, err := ft.Subset(Range{Start: 1500, Length: 2000})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 2)
		require.Equal(t, FrameSize{U: 2000, C: 1000}, subset.Frames[0])
		require.Equal(t, FrameSize{U: 1500, C: 750}, subset.Frames[1])
	})

	t.Run("subset ending in middle of frame", func(t *testing.T) {
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
		// Request range [8000, 100) - start beyond total size, no frames included
		subset, err := ft.Subset(Range{Start: 8000, Length: 100})
		require.Contains(t, err.Error(), "requested range is beyond the end of the frame table")
		require.Nil(t, subset)
	})

	t.Run("subset with zero length is nil", func(t *testing.T) {
		// Request range [1000, 0) - zero length, should return empty subset
		subset, err := ft.Subset(Range{Start: 1000, Length: 0})
		require.NoError(t, err)
		require.Nil(t, subset)
	})

	t.Run("subset single byte at start", func(t *testing.T) {
		// Request range [0, 1) - single byte, needs entire first frame
		subset, err := ft.Subset(Range{Start: 100, Length: 1})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, FrameSize{U: 1000, C: 500}, subset.Frames[0])
	})

	t.Run("subset single byte in middle", func(t *testing.T) {
		// Request range [2500, 1) - single byte in frame 1, needs entire frame
		subset, err := ft.Subset(Range{Start: 2500, Length: 1})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, FrameSize{U: 2000, C: 1000}, subset.Frames[0])
	})

	t.Run("subset single byte at end", func(t *testing.T) {
		// Request range [7499, 1) - last byte, needs entire last frame
		subset, err := ft.Subset(Range{Start: 7499, Length: 1})
		require.NoError(t, err)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, FrameSize{U: 3000, C: 1500}, subset.Frames[0])
	})

	t.Run("subset preserves compression type", func(t *testing.T) {
		// Verify compression type is preserved
		subset, err := ft.Subset(Range{Start: 100, Length: 1000})
		require.NoError(t, err)
		require.Equal(t, CompressionZstd, subset.CompressionType)
	})

	t.Run("subset with large frames", func(t *testing.T) {
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
		var nilCi *FrameTable
		subset, err := nilCi.Subset(Range{Start: 0, Length: 100})
		require.NoError(t, err)
		require.Nil(t, subset)
	})

	t.Run("Subset starts before the start of the frameTable", func(t *testing.T) {
		_, err := ft.Subset(Range{Start: 50, Length: 100})
		require.Contains(t, err.Error(), "requested range starts before the beginning of the frame table")
	})
}
