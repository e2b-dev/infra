package storage

import (
	"encoding/xml"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
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

	r := rand.New(rand.NewSource(42))

	uncompressedSize := 0
	compressedSize := 0
	var origData []byte
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

	mu := &sync.Mutex{}
	var uploadID string
	var initiateC int
	var completeC int
	receivedParts := make(map[string][]byte)
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
			require.NotEmpty(t, partNum)

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
	fe, err := newFrameEncoder(CompressionZstd, zstdCompressionLevel, 0, chunkSize, frameSize, fu.handleFrame)
	require.NoError(t, err)

	// 171 and newVectorReader (a "pure" io.Reader) to exercise uneven chunking
	// in fe.Write
	fi, err := multipartCompressUploadFile(newVectorReader([][]byte{origData}), fe, fu, 171)
	require.NoError(t, err)
	require.Equal(t, 1, initiateC)
	require.Equal(t, 1, completeC)
	require.Greater(t, len(receivedParts), 3, "should have been at least 4 parts uploaded")

	fiTotalUncompressed := 0
	for _, frame := range fi {
		fiTotalUncompressed += frame.Uncompressed
		require.LessOrEqual(t, frame.Compressed, frame.Uncompressed,
			"expect that all frames get somewhat compressed due to the nature of the data")
		require.Equal(t, 0, frame.Uncompressed%chunkSize,
			"expect each frame's uncompressed size to be multiple of chunk size")
	}
	require.Equal(t, uncompressedSize, fiTotalUncompressed,
		"expect total uncompressed size in frame info to match original data length")

	// Verify uploaded parts
	var data []byte
	for i := range len(receivedParts) {
		partNum := i + 1
		partData, ok := receivedParts[fmt.Sprintf("%d", partNum)]
		require.True(t, ok, "missing part %d", partNum)

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
}
