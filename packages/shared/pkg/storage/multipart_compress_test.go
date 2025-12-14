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
		partSize  = 4096 + 512 // extra to accommodate slightly bigger frames

		targetNumParts    = 5
		maxChunksPerFrame = 5
	)

	r := rand.New(rand.NewSource(42))

	var f *syncBuffer
	var zw *zstd.Encoder
	restartEncoder := func() []byte {
		var encErr error
		if zw != nil {
			encErr = zw.Close()
			require.NoError(t, encErr)
		}

		var bb []byte
		if f != nil {
			bb = f.Bytes()
		}
		f = newSyncBuffer(frameSize + chunkSize)

		zw, encErr = newZstdEncoder(f, 0, frameSize, zstdCompressionLevel)
		require.NoError(t, encErr)

		return bb
	}
	restartEncoder()
	defer zw.Close()

	// Save all the original data to verify after upload
	uncompressedSize, compressedSize := 0, 0
	var origData []byte
	var origFrames [][]byte
	iPart := 0
	var part []byte
	var chunksInFrame int
	var chunksInPart int
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
		chunksInPart++

		// add to compressed frame
		_, err := zw.Write(chunk)
		require.NoError(t, err)

		if f.Len() >= frameSize || chunksInFrame >= maxChunksPerFrame {
			bb := restartEncoder()

			origFrames = append(origFrames, bb)
			part = append(part, bb...)
			compressedSize += len(bb)
			chunksInFrame = 0

			if len(part) >= partSize {
				iPart++
				part = nil
				chunksInPart = 0
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
	fe, err := newFrameEncoder(CompressionZstd, zstdCompressionLevel, zstdDefaultConcurrency, chunkSize, frameSize, fu.handleFrame)
	require.NoError(t, err)

	// 171 and newVectorReader (a "pure" io.Reader) to exercise uneven chunking
	// in fe.Write
	err = multipartCompressUploadFile(newVectorReader([][]byte{origData}), fe, fu, 171)
	require.NoError(t, err)
	require.Equal(t, 1, initiateC)
	require.Equal(t, 1, completeC)
	require.Greater(t, len(receivedParts), 0, "no parts were uploaded")

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
	require.Equal(t, origData, data, "uploaded data does not match original")
}
