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

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultipartCompressUploadFile_Success(t *testing.T) {
	const (
		chunkSize = 1024
		frameSize = 2048
		partSize  = 4096

		targetNumParts    = 5
		maxChunksPerFrame = 5
	)

	uncompressedSize := 0
	var origData []byte
	r := rand.New(rand.NewSource(42))

	t.Run("Generate test data", func(t *testing.T) {
		compressedSize := 0
		iPart := 0
		iFrame := 0
		var chunksInFrame int
		var bytesInPart int
		for iPart < targetNumParts {
			// read in some random bytes
			chunk := make([]byte, 0, chunkSize)
			for len(chunk) < chunkSize {
				n := r.Intn(8) + 1
				b := r.Intn(256)
				for range n {
					chunk = append(chunk, byte(b))
				}
			}
			chunk = chunk[:chunkSize]
			origData = append(origData, chunk...)
			uncompressedSize += len(chunk)
			chunksInFrame++

			compressedBuf := newSyncBuffer(frameSize + chunkSize)
			zw, err := zstd.NewWriter(compressedBuf, zstd.WithEncoderLevel(zstdCompressionLevel))
			require.NoError(t, err)
			_, err = zw.Write(chunk)
			require.NoError(t, err)
			err = zw.Close()
			require.NoError(t, err)

			// see if we need to start a new frame
			bb := compressedBuf.Bytes()
			compressedSize += len(bb)
			if chunksInFrame >= maxChunksPerFrame || len(bb) >= frameSize {
				iFrame++
				chunksInFrame = 0
				if bytesInPart+len(bb) >= partSize {
					iPart++
					bytesInPart = 0
				} else {
					bytesInPart += len(bb)
				}
			}
		}
	})

	var data []byte
	var ci *CompressedInfo
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

				w.Header().Set("ETag", fmt.Sprintf(`"etag%s"`, partNum))
				w.WriteHeader(http.StatusOK)

			case strings.Contains(r.URL.RawQuery, "uploadId"):
				// Complete upload
				completeC++
				w.WriteHeader(http.StatusOK)
			}
		})

		uploader := createTestMultipartUploader(t, handler)
		fu, err := newFrameUploader(t.Context(), uploader, partSize, 3)
		require.NoError(t, err)
		opts := CompressionOptions{
			CompressionType: CompressionZstd,
			Level:           int(zstdCompressionLevel),
			ChunkSize:       chunkSize,
			TargetFrameSize: frameSize,
		}

		fe, err := newFrameEncoder(&opts, fu.handleFrame)
		require.NoError(t, err)

		// 171 and newPureReader (a "pure" io.Reader) to exercise uneven chunking
		// in fe.Write
		ci, err = multipartCompressUploadFile(newPureReader(origData), fe, fu, 171)
		require.NoError(t, err)
		require.Equal(t, 1, initiateC)
		require.Equal(t, 1, completeC)
		require.Greater(t, len(receivedParts), 3, "should have been at least 4 parts uploaded")

		totalUncompressed := 0
		for _, frame := range ci.Frames {
			totalUncompressed += frame.U
			require.LessOrEqual(t, frame.C, frame.U,
				"expect that all frames get somewhat compressed due to the nature of the data")
			require.Equal(t, 0, frame.U%chunkSize,
				"expect each frame's uncompressed size to be multiple of chunk size")
		}
		require.Equal(t, uncompressedSize, totalUncompressed,
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
		t.Logf("original data %d bytes, received data %d bytes\n", len(origData), len(receivedData))
		rr := &fakeRanger{data: receivedData}

		for range 10 {
			s := r.Intn(len(origData) - 1)
			e := r.Intn(len(origData) - 1)
			if s > e {
				s, e = e, s
			}

			buf := make([]byte, e-s)
			t.Logf("requesting slice from %d to %d, %d bytes\n", s, e, len(buf))

			err := ci.DownloadSlice(t.Context(), rr, buf, int64(s))
			require.NoError(t, err)
			require.Equal(t, origData[s:e], buf)
		}
	})
}

type fakeRanger struct {
	data []byte
}

func (r *fakeRanger) RangeGet(_ context.Context, offset, length int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.data[offset : offset+length])), nil
}

type pureReader struct {
	data []byte
}

func newPureReader(data []byte) *pureReader {
	return &pureReader{
		data: data,
	}
}

func (mr *pureReader) Read(p []byte) (n int, err error) {
	if len(mr.data) == 0 {
		return 0, io.EOF
	}

	if len(mr.data) < len(p) {
		copy(p, mr.data)
		n = len(mr.data)
		mr.data = nil

		return n, io.EOF
	}

	copy(p, mr.data[:len(p)])
	mr.data = mr.data[len(p):]

	return len(p), nil
}

func TestCompressedInfo_Subset(t *testing.T) {
	// Create a CompressedInfo with frames of known sizes
	// Frame 0: U=1000, C=500 (offset 0-1000)
	// Frame 1: U=2000, C=1000 (offset 1000-3000)
	// Frame 2: U=1500, C=750 (offset 3000-4500)
	// Frame 3: U=3000, C=1500 (offset 4500-7500)
	// Total uncompressed: 7500 bytes
	ci := &CompressedInfo{
		CompressionType: CompressionZstd,
		FramesStartAt:   Offset{U: 100, C: 50},
		Frames: []Frame{
			{U: 1000, C: 500},
			{U: 2000, C: 1000},
			{U: 1500, C: 750},
			{U: 3000, C: 1500},
		},
	}

	t.Run("subset at the beginning", func(t *testing.T) {
		// Request range [0, 500) - should include only frame 0
		subset := ci.Subset(0, 500)
		require.NotNil(t, subset)
		require.Equal(t, CompressionZstd, subset.CompressionType)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 1000, C: 500}, subset.Frames[0])
	})

	t.Run("subset exactly one frame", func(t *testing.T) {
		// Request range [1000, 2000) - should include frame 1
		subset := ci.Subset(1000, 2000)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 2000, C: 1000}, subset.Frames[0])
	})

	t.Run("subset spanning multiple frames", func(t *testing.T) {
		// Request range [500, 3500) - should include frames 0, 1, 2
		subset := ci.Subset(500, 3000)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 3)
		require.Equal(t, Frame{U: 1000, C: 500}, subset.Frames[0])
		require.Equal(t, Frame{U: 2000, C: 1000}, subset.Frames[1])
		require.Equal(t, Frame{U: 1500, C: 750}, subset.Frames[2])
	})

	t.Run("subset at the end", func(t *testing.T) {
		// Request range [6000, 1500) - should include only frame 3
		subset := ci.Subset(6000, 1500)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 3000, C: 1500}, subset.Frames[0])
	})

	t.Run("subset entire range", func(t *testing.T) {
		// Request range [0, 7500) - should include all frames
		subset := ci.Subset(0, 7500)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 4)
		require.Equal(t, ci.Frames, subset.Frames)
	})

	t.Run("subset with frame boundaries aligned", func(t *testing.T) {
		// Request range [1000, 3000) - exactly frames 1 and 2
		subset := ci.Subset(1000, 3000)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 2)
		require.Equal(t, Frame{U: 2000, C: 1000}, subset.Frames[0])
		require.Equal(t, Frame{U: 1500, C: 750}, subset.Frames[1])
	})

	t.Run("subset starting in middle of frame", func(t *testing.T) {
		// Request range [1500, 2000) - starts in frame 1, needs entire frames 1 and 2
		subset := ci.Subset(1500, 2000)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 2)
		require.Equal(t, Frame{U: 2000, C: 1000}, subset.Frames[0])
		require.Equal(t, Frame{U: 1500, C: 750}, subset.Frames[1])
	})

	t.Run("subset ending in middle of frame", func(t *testing.T) {
		// Request range [3000, 500) - ends in frame 2, includes frame 2
		subset := ci.Subset(3000, 500)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 1500, C: 750}, subset.Frames[0])
	})

	t.Run("subset beyond total size stops at end", func(t *testing.T) {
		// Request range [7000, 1000) - end exceeds total size, stops at frame 3
		subset := ci.Subset(7000, 1000)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 3000, C: 1500}, subset.Frames[0])
	})

	t.Run("subset starting beyond total size returns empty", func(t *testing.T) {
		// Request range [8000, 100) - start beyond total size, no frames included
		subset := ci.Subset(8000, 100)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 0)
	})

	t.Run("subset with zero length", func(t *testing.T) {
		// Request range [1000, 0) - zero length, should return empty subset
		subset := ci.Subset(1000, 0)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 0)
	})

	t.Run("subset single byte at start", func(t *testing.T) {
		// Request range [0, 1) - single byte, needs entire first frame
		subset := ci.Subset(0, 1)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 1000, C: 500}, subset.Frames[0])
	})

	t.Run("subset single byte in middle", func(t *testing.T) {
		// Request range [2500, 1) - single byte in frame 1, needs entire frame
		subset := ci.Subset(2500, 1)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 2000, C: 1000}, subset.Frames[0])
	})

	t.Run("subset single byte at end", func(t *testing.T) {
		// Request range [7499, 1) - last byte, needs entire last frame
		subset := ci.Subset(7499, 1)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 1)
		require.Equal(t, Frame{U: 3000, C: 1500}, subset.Frames[0])
	})

	t.Run("subset preserves compression type", func(t *testing.T) {
		// Verify compression type is preserved
		subset := ci.Subset(0, 1000)
		require.Equal(t, CompressionZstd, subset.CompressionType)
	})

	t.Run("subset with large frames", func(t *testing.T) {
		// Create CompressedInfo with larger frames
		largeCi := &CompressedInfo{
			CompressionType: CompressionZstd,
			Frames: []Frame{
				{U: 1000000, C: 500000},  // 1MB uncompressed
				{U: 2000000, C: 1000000}, // 2MB uncompressed
				{U: 1000000, C: 500000},  // 1MB uncompressed
			},
		}

		// Request middle portion
		subset := largeCi.Subset(500000, 2000000)
		require.Len(t, subset.Frames, 2)
		require.Equal(t, Frame{U: 1000000, C: 500000}, subset.Frames[0])
		require.Equal(t, Frame{U: 2000000, C: 1000000}, subset.Frames[1])
	})

	t.Run("subset from empty CompressedInfo returns empty", func(t *testing.T) {
		emptyCi := &CompressedInfo{
			CompressionType: CompressionZstd,
			Frames:          []Frame{},
		}

		// Request returns empty subset
		subset := emptyCi.Subset(0, 100)
		require.NotNil(t, subset)
		require.Len(t, subset.Frames, 0)
	})

	t.Run("subset beyond end stops gracefully", func(t *testing.T) {
		totalSize := ci.TotalUncompressedSize()
		require.Equal(t, int64(7500), totalSize)

		// Requesting exactly the total size should work
		subset := ci.Subset(0, int64(totalSize))
		require.Len(t, subset.Frames, 4)

		// Requesting one byte more stops at the end
		subset = ci.Subset(0, int64(totalSize)+1)
		require.Len(t, subset.Frames, 4)
	})

	t.Run("Subset of nil is nil", func(t *testing.T) {
		var nilCi *CompressedInfo
		subset := nilCi.Subset(0, 100)
		require.Nil(t, subset)
	})
}
