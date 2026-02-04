package storage

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Test constants
const (
	testBucketName = "test-bucket"
	testObjectName = "test-object"
	testToken      = "test-token"
	uploadsPath    = "uploads"
)

// createTestMultipartUploader creates a test uploader with a mock HTTP client
func createTestMultipartUploader(t *testing.T, handler http.HandlerFunc, retryConfig ...RetryConfig) *MultipartUploader {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	config := DefaultRetryConfig()
	if len(retryConfig) > 0 {
		config = retryConfig[0]
	}

	// Create retryable client using the test server's client
	retryableClient := createRetryableClient(t.Context(), config)
	retryableClient.HTTPClient = server.Client()

	uploader := &MultipartUploader{
		bucketName:  testBucketName,
		objectName:  testObjectName,
		token:       testToken,
		client:      retryableClient,
		retryConfig: config,
		baseURL:     server.URL, // Override to use test server
	}

	return uploader
}

func TestMultipartUploader_InitiateUpload_Success(t *testing.T) {
	t.Parallel()
	expectedUploadID := "test-upload-id-123"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, testObjectName)
		assert.Contains(t, r.URL.RawQuery, uploadsPath)
		assert.Equal(t, "Bearer "+testToken, r.Header.Get("Authorization"))
		assert.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))

		response := InitiateMultipartUploadResult{
			Bucket:   testBucketName,
			Key:      testObjectName,
			UploadID: expectedUploadID,
		}

		xmlData, _ := xml.Marshal(response)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		w.Write(xmlData)
	})

	uploader := createTestMultipartUploader(t, handler)
	uploadID, err := uploader.initiateUpload(t.Context())

	require.NoError(t, err)
	require.Equal(t, expectedUploadID, uploadID)
}

func TestMultipartUploader_UploadPart_Success(t *testing.T) {
	t.Parallel()
	expectedETag := `"abcd1234"`
	testData := []byte("test part data")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Contains(t, r.URL.RawQuery, "partNumber=1")
		assert.Contains(t, r.URL.RawQuery, "uploadId=test-upload-id")
		assert.Equal(t, "Bearer "+testToken, r.Header.Get("Authorization"))

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, testData, body)

		w.Header().Set("ETag", expectedETag)
		w.WriteHeader(http.StatusOK)
	})

	uploader := createTestMultipartUploader(t, handler)
	etag, err := uploader.uploadPart(t.Context(), "test-upload-id", 1, testData)

	require.NoError(t, err)
	require.Equal(t, expectedETag, etag)
}

func TestMultipartUploader_UploadPart_MissingETag(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Don't set ETag header
		w.WriteHeader(http.StatusOK)
	})

	uploader := createTestMultipartUploader(t, handler)
	etag, err := uploader.uploadPart(t.Context(), "test-upload-id", 1, []byte("test"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "no ETag returned for part 1")
	require.Empty(t, etag)
}

func TestMultipartUploader_CompleteUpload_Success(t *testing.T) {
	t.Parallel()
	parts := []Part{
		{PartNumber: 1, ETag: `"etag1"`},
		{PartNumber: 2, ETag: `"etag2"`},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.RawQuery, "uploadId=test-upload-id")
		assert.Equal(t, "Bearer "+testToken, r.Header.Get("Authorization"))
		assert.Equal(t, "application/xml", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)

		var completeReq CompleteMultipartUpload
		err = xml.Unmarshal(body, &completeReq)
		assert.NoError(t, err)
		assert.Len(t, completeReq.Parts, 2)
		assert.Equal(t, 1, completeReq.Parts[0].PartNumber)
		assert.Equal(t, `"etag1"`, completeReq.Parts[0].ETag)

		w.WriteHeader(http.StatusOK)
	})

	uploader := createTestMultipartUploader(t, handler)
	err := uploader.completeUpload(t.Context(), "test-upload-id", parts)
	require.NoError(t, err)
}

func TestMultipartUploader_UploadFileInParallel_Success(t *testing.T) {
	t.Parallel()
	// Create a test file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := strings.Repeat("test data content ", 1000) // Make it large enough for multiple chunks
	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	var uploadID string
	var initiateCount, uploadPartCount, completeCount int32
	receivedParts := make(map[int]string)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			// Initiate upload
			atomic.AddInt32(&initiateCount, 1)
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

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			// Upload part
			partNum := atomic.AddInt32(&uploadPartCount, 1)
			body, _ := io.ReadAll(r.Body)
			receivedParts[int(partNum)] = string(body)

			w.Header().Set("ETag", fmt.Sprintf(`"etag%d"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			// Complete upload
			atomic.AddInt32(&completeCount, 1)
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestMultipartUploader(t, handler)
	_, err = uploader.UploadFileInParallel(t.Context(), testFile, 2)
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&initiateCount))
	require.Equal(t, int32(1), atomic.LoadInt32(&completeCount))
	require.Positive(t, atomic.LoadInt32(&uploadPartCount))

	// Verify all parts were uploaded and content matches
	var reconstructed strings.Builder
	for i := 1; i <= int(atomic.LoadInt32(&uploadPartCount)); i++ {
		reconstructed.WriteString(receivedParts[i])
	}
	require.Equal(t, testContent, reconstructed.String())
}

func TestMultipartUploader_InitiateUpload_WithRetries(t *testing.T) {
	t.Parallel()
	var requestCount int32
	expectedUploadID := "retry-upload-id"

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if count < 2 {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		response := InitiateMultipartUploadResult{
			Bucket:   testBucketName,
			Key:      testObjectName,
			UploadID: expectedUploadID,
		}
		xmlData, _ := xml.Marshal(response)
		w.WriteHeader(http.StatusOK)
		w.Write(xmlData)
	})

	config := RetryConfig{
		MaxAttempts:       3,
		InitialBackoff:    10 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2,
	}

	uploader := createTestMultipartUploader(t, handler, config)
	uploadID, err := uploader.initiateUpload(t.Context())

	require.NoError(t, err)
	require.Equal(t, expectedUploadID, uploadID)
	require.Equal(t, int32(2), atomic.LoadInt32(&requestCount))
}

// STRESS TESTS AND EDGE CASES

func TestMultipartUploader_HighConcurrency_StressTest(t *testing.T) {
	t.Parallel()
	// Create a large test file (200MB - enough for 4 parts)
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "large.txt")
	testContent := strings.Repeat("0123456789abcdef", 6553600) // 100MB file
	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	var initiateCalls, partCalls, completeCalls int32
	var maxConcurrentParts int32
	var currentConcurrentParts int32
	receivedParts := sync.Map{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			atomic.AddInt32(&initiateCalls, 1)
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "stress-test-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			// Track concurrent part uploads
			current := atomic.AddInt32(&currentConcurrentParts, 1)
			defer atomic.AddInt32(&currentConcurrentParts, -1)

			// Update max concurrent parts
			for {
				maxConcurrent := atomic.LoadInt32(&maxConcurrentParts)
				if current <= maxConcurrent || atomic.CompareAndSwapInt32(&maxConcurrentParts, maxConcurrent, current) {
					break
				}
			}

			// Simulate some processing time to increase chance of concurrency
			time.Sleep(50 * time.Millisecond) // Increased delay to ensure overlap

			partNum := atomic.AddInt32(&partCalls, 1)
			body, _ := io.ReadAll(r.Body)
			receivedParts.Store(int(partNum), string(body))

			w.Header().Set("ETag", fmt.Sprintf(`"etag%d"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			atomic.AddInt32(&completeCalls, 1)
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestMultipartUploader(t, handler)

	// Use high concurrency to stress test
	_, err = uploader.UploadFileInParallel(t.Context(), testFile, 50)
	require.NoError(t, err)

	// Verify all calls were made
	require.Equal(t, int32(1), atomic.LoadInt32(&initiateCalls))
	require.Equal(t, int32(1), atomic.LoadInt32(&completeCalls))
	require.Positive(t, atomic.LoadInt32(&partCalls))
	require.Greater(t, atomic.LoadInt32(&maxConcurrentParts), int32(1), "Should have concurrent uploads")

	// Verify content integrity
	var reconstructed strings.Builder
	for i := 1; i <= int(atomic.LoadInt32(&partCalls)); i++ {
		if part, ok := receivedParts.Load(i); ok {
			reconstructed.WriteString(part.(string))
		}
	}
	require.Equal(t, testContent, reconstructed.String())
}

func TestMultipartUploader_RandomFailures_ChaosTest(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "chaos.txt")
	testContent := strings.Repeat("chaos test data ", 3200000) // ~100MB file for multiple parts
	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	var attemptCount, successCount int32
	failureRate := 0.3 // 30% failure rate

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "chaos-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			atomic.AddInt32(&attemptCount, 1)

			// Randomly fail some requests
			if rand.Float64() < failureRate {
				w.WriteHeader(http.StatusInternalServerError)

				return
			}

			atomic.AddInt32(&successCount, 1)
			partNum := strings.Split(strings.Split(r.URL.RawQuery, "partNumber=")[1], "&")[0]
			w.Header().Set("ETag", fmt.Sprintf(`"chaos-etag-%s"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			w.WriteHeader(http.StatusOK)
		}
	})

	// Use aggressive retry config for chaos test
	config := RetryConfig{
		MaxAttempts:       10,
		InitialBackoff:    1 * time.Millisecond,
		MaxBackoff:        100 * time.Millisecond,
		BackoffMultiplier: 2,
	}

	uploader := createTestMultipartUploader(t, handler, config)
	_, err = uploader.UploadFileInParallel(t.Context(), testFile, 10)
	require.NoError(t, err)

	t.Logf("Chaos test: %d total attempts, %d successes",
		atomic.LoadInt32(&attemptCount), atomic.LoadInt32(&successCount))

	// Should have more attempts than successes due to retries
	require.GreaterOrEqual(t, atomic.LoadInt32(&attemptCount), atomic.LoadInt32(&successCount))
}

func TestMultipartUploader_PartialFailures_Recovery(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "partial.txt")
	testContent := strings.Repeat("partial failure test ", 2500000) // 100MB+ file
	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	var partAttempts sync.Map
	maxAttempts := 3

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "partial-fail-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			partNumStr := strings.Split(strings.Split(r.URL.RawQuery, "partNumber=")[1], "&")[0]

			// Track attempts per part
			val, _ := partAttempts.LoadOrStore(partNumStr, utils.ToPtr(int32(0)))
			attempts := val.(*int32)
			currentAttempts := atomic.AddInt32(attempts, 1)

			// Fail first few attempts for each part, then succeed
			if currentAttempts < int32(maxAttempts-1) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("simulated failure"))

				return
			}

			w.Header().Set("ETag", fmt.Sprintf(`"recovery-etag-%s"`, partNumStr))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			w.WriteHeader(http.StatusOK)
		}
	})

	config := RetryConfig{
		MaxAttempts:       maxAttempts,
		InitialBackoff:    5 * time.Millisecond,
		MaxBackoff:        50 * time.Millisecond,
		BackoffMultiplier: 2,
	}

	uploader := createTestMultipartUploader(t, handler, config)
	_, err = uploader.UploadFileInParallel(t.Context(), testFile, 5)
	require.NoError(t, err)

	// Verify that all parts eventually succeeded after retries
	partAttempts.Range(func(key, value any) bool {
		attempts := atomic.LoadInt32(value.(*int32))
		require.Equal(t, int32(maxAttempts-1), attempts, "Part %s should have exactly %d attempts", key, maxAttempts-1)

		return true
	})
}

func TestMultipartUploader_EdgeCases_EmptyFile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	emptyFile := filepath.Join(tempDir, "empty.txt")
	err := os.WriteFile(emptyFile, []byte(""), 0o644)
	require.NoError(t, err)

	var initiateCalls, partCalls, completeCalls int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			atomic.AddInt32(&initiateCalls, 1)
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "empty-file-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			atomic.AddInt32(&partCalls, 1)
			body, _ := io.ReadAll(r.Body)
			assert.Empty(t, body, "Empty file should result in empty part")

			w.Header().Set("ETag", `"empty-etag"`)
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			atomic.AddInt32(&completeCalls, 1)
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestMultipartUploader(t, handler)
	_, err = uploader.UploadFileInParallel(t.Context(), emptyFile, 5)
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&initiateCalls))
	require.Equal(t, int32(1), atomic.LoadInt32(&partCalls)) // Should have exactly 1 part for empty file
	require.Equal(t, int32(1), atomic.LoadInt32(&completeCalls))
}

func TestMultipartUploader_EdgeCases_VerySmallFile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	smallFile := filepath.Join(tempDir, "small.txt")
	smallContent := "small"
	err := os.WriteFile(smallFile, []byte(smallContent), 0o644)
	require.NoError(t, err)

	var receivedData string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "small-file-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			body, _ := io.ReadAll(r.Body)
			receivedData = string(body)

			w.Header().Set("ETag", `"small-etag"`)
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestMultipartUploader(t, handler)
	_, err = uploader.UploadFileInParallel(t.Context(), smallFile, 10) // High concurrency for small file
	require.NoError(t, err)
	require.Equal(t, smallContent, receivedData)
}

type repeatReader struct {
	char      byte
	remaining int
}

var _ io.Reader = (*repeatReader)(nil)

func (r *repeatReader) Read(p []byte) (n int, err error) {
	toWrite := int(math.Min(float64(len(p)), float64(r.remaining)))
	if toWrite <= 0 {
		return 0, io.EOF
	}
	r.remaining -= toWrite

	for index := range p[:toWrite] {
		p[index] = r.char
	}

	return toWrite, nil
}

func newRepeatReader(b byte, count int) io.Reader {
	return &repeatReader{char: b, remaining: count}
}

func TestMultipartUploader_ResourceExhaustion_TooManyConcurrentUploads(t *testing.T) {
	t.Parallel()

	totalChunks := 10

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "resource.txt")
	file, err := os.Create(testFile)
	require.NoError(t, err)
	count, err := io.Copy(file, newRepeatReader('a', gcpMultipartUploadChunkSize*totalChunks))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, int64(gcpMultipartUploadChunkSize*totalChunks))
	err = file.Close()
	require.NoError(t, err)

	var activeConcurrency atomic.Int32
	var maxObservedConcurrency atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "resource-test-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			current := activeConcurrency.Add(1)
			defer activeConcurrency.Add(-1)

			// Track max observed concurrency
			for {
				maxObserved := maxObservedConcurrency.Load()
				if current <= maxObserved || maxObservedConcurrency.CompareAndSwap(maxObserved, current) {
					break
				}
			}

			// Simulate work that takes time
			time.Sleep(10 * time.Millisecond)

			partNum := strings.Split(strings.Split(r.URL.RawQuery, "partNumber=")[1], "&")[0]
			w.Header().Set("ETag", fmt.Sprintf(`"resource-etag-%s"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestMultipartUploader(t, handler)

	// Try with extremely high concurrency
	_, err = uploader.UploadFileInParallel(t.Context(), testFile, 1000)
	require.NoError(t, err)

	// Should have observed significant concurrency but not necessarily 1000
	// (due to file size and chunk limitations)
	t.Logf("Max observed concurrency: %d", maxObservedConcurrency.Load())
	require.Greater(t, maxObservedConcurrency.Load(), int32(1))
}

func TestMultipartUploader_BoundaryConditions_ExactChunkSize(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "exact.txt")
	// Create file that's exactly 2 chunks
	testContent := strings.Repeat("x", gcpMultipartUploadChunkSize*2)
	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	var partSizes []int

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "boundary-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			body, _ := io.ReadAll(r.Body)
			partSizes = append(partSizes, len(body))

			partNum := strings.Split(strings.Split(r.URL.RawQuery, "partNumber=")[1], "&")[0]
			w.Header().Set("ETag", fmt.Sprintf(`"boundary-etag-%s"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestMultipartUploader(t, handler)
	_, err = uploader.UploadFileInParallel(t.Context(), testFile, 5)
	require.NoError(t, err)

	// Should have exactly 2 parts, each of ChunkSize
	require.Len(t, partSizes, 2)
	require.Equal(t, gcpMultipartUploadChunkSize, partSizes[0])
	require.Equal(t, gcpMultipartUploadChunkSize, partSizes[1])
}

func TestMultipartUploader_FileNotFound_Error(t *testing.T) {
	t.Parallel()
	uploader := createTestMultipartUploader(t, func(http.ResponseWriter, *http.Request) {
		t.Error("Should not make any HTTP requests for missing file")
	})

	_, err := uploader.UploadFileInParallel(t.Context(), "/nonexistent/file.txt", 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to open file")
}

func TestMultipartUploader_ConcurrentRetries_RaceCondition(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "race.txt")
	testContent := strings.Repeat("race condition test ", 2500000) // 100MB+ file
	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	var retryAttempts sync.Map
	var totalRequests int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&totalRequests, 1)

		switch {
		case r.URL.RawQuery == uploadsPath:
			response := InitiateMultipartUploadResult{
				Bucket:   testBucketName,
				Key:      testObjectName,
				UploadID: "race-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			partNumStr := strings.Split(strings.Split(r.URL.RawQuery, "partNumber=")[1], "&")[0]

			// Track retry attempts per part with race-safe operations
			val, _ := retryAttempts.LoadOrStore(partNumStr, utils.ToPtr(int32(0)))
			attempts := val.(*int32)
			currentAttempt := atomic.AddInt32(attempts, 1)

			// Fail first 2 attempts to force retries under high concurrency
			if currentAttempt <= 2 {
				// Add random delay to increase race condition probability
				time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
				w.WriteHeader(http.StatusInternalServerError)

				return
			}

			w.Header().Set("ETag", fmt.Sprintf(`"race-etag-%s"`, partNumStr))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			w.WriteHeader(http.StatusOK)
		}
	})

	config := RetryConfig{
		MaxAttempts:       5,
		InitialBackoff:    1 * time.Millisecond, // Very fast retries to increase race probability
		MaxBackoff:        10 * time.Millisecond,
		BackoffMultiplier: 2,
	}

	uploader := createTestMultipartUploader(t, handler, config)
	_, err = uploader.UploadFileInParallel(t.Context(), testFile, 20) // High concurrency
	require.NoError(t, err)

	t.Logf("Total HTTP requests made: %d", atomic.LoadInt32(&totalRequests))

	// Verify that retries happened correctly under concurrent conditions
	retryAttempts.Range(func(key, value any) bool {
		attempts := atomic.LoadInt32(value.(*int32))
		require.GreaterOrEqual(t, attempts, int32(3), "Part %s should have at least 3 attempts", key)

		return true
	})
}

// TestCreateRetryableClient_JitterBehavior tests that the jittered backoff works correctly
func TestCreateRetryableClient_JitterBehavior(t *testing.T) {
	t.Parallel()
	config := RetryConfig{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
	}

	client := createRetryableClient(t.Context(), config)
	require.NotNil(t, client)
	require.NotNil(t, client.Backoff)

	// Test jitter produces values within expected range
	t.Run("JitterRange", func(t *testing.T) {
		t.Parallel()
		// Test first attempt (attemptNum = 0)
		for range 10 {
			backoff := client.Backoff(config.InitialBackoff, config.MaxBackoff, 0, nil)
			require.GreaterOrEqual(t, backoff, time.Duration(0))
			require.Less(t, backoff, config.InitialBackoff)
		}

		// Test second attempt (attemptNum = 1) - should be jittered version of 200ms
		expectedBase := time.Duration(float64(config.InitialBackoff) * config.BackoffMultiplier)
		for range 10 {
			backoff := client.Backoff(config.InitialBackoff, config.MaxBackoff, 1, nil)
			require.GreaterOrEqual(t, backoff, time.Duration(0))
			require.Less(t, backoff, expectedBase)
		}
	})

	// Test that jitter produces different values (randomness)
	t.Run("JitterRandomness", func(t *testing.T) {
		t.Parallel()
		values := make(map[time.Duration]bool)

		// Collect 20 jittered values
		for range 20 {
			backoff := client.Backoff(config.InitialBackoff, config.MaxBackoff, 1, nil)
			values[backoff] = true
		}

		// Should have at least some variation (not all the same value)
		// With a range of 0-200ms, getting 20 identical values is highly unlikely
		require.Greater(t, len(values), 1, "Jitter should produce varied values")
	})

	// Test exponential backoff base calculation (before jitter)
	t.Run("ExponentialBackoffBase", func(t *testing.T) {
		t.Parallel()
		// We can't directly test the base calculation due to jitter,
		// but we can verify the max possible value matches our expectation

		// For attemptNum=0: base should be 100ms, jitter: 0-100ms
		// For attemptNum=1: base should be 200ms, jitter: 0-200ms
		// For attemptNum=2: base should be 400ms, jitter: 0-400ms

		// Test attempt 2 multiple times and verify max range
		var maxSeen time.Duration
		for range 100 {
			backoff := client.Backoff(config.InitialBackoff, config.MaxBackoff, 2, nil)
			if backoff > maxSeen {
				maxSeen = backoff
			}
		}

		expectedBase := time.Duration(float64(config.InitialBackoff) * config.BackoffMultiplier * config.BackoffMultiplier)
		// The max we should ever see is just under the expected base (due to jitter being 0 to base-1)
		require.Less(t, maxSeen, expectedBase)
		// But we should see values reasonably close to the base in 100 attempts
		require.Greater(t, maxSeen, expectedBase/2)
	})

	// Test max backoff cap
	t.Run("MaxBackoffCap", func(t *testing.T) {
		t.Parallel()
		// With high attempt numbers, backoff should be capped at MaxBackoff
		for range 10 {
			backoff := client.Backoff(config.InitialBackoff, config.MaxBackoff, 10, nil)
			require.GreaterOrEqual(t, backoff, time.Duration(0))
			require.Less(t, backoff, config.MaxBackoff)
		}
	})
}

// TestCreateRetryableClient_Configuration tests the retry client configuration
func TestCreateRetryableClient_Configuration(t *testing.T) {
	t.Parallel()
	config := RetryConfig{
		MaxAttempts:       5,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        2 * time.Second,
		BackoffMultiplier: 3.0,
	}

	client := createRetryableClient(t.Context(), config)

	// Verify retry configuration
	require.Equal(t, config.MaxAttempts-1, client.RetryMax) // go-retryablehttp counts retries, not total attempts
	require.Equal(t, config.InitialBackoff, client.RetryWaitMin)
	require.Equal(t, config.MaxBackoff, client.RetryWaitMax)
	require.NotNil(t, client.Logger)
	require.NotNil(t, client.Backoff)
}

// TestCreateRetryableClient_ZeroBackoff tests edge case of zero backoff
func TestCreateRetryableClient_ZeroBackoff(t *testing.T) {
	t.Parallel()
	config := RetryConfig{
		MaxAttempts:       2,
		InitialBackoff:    0, // Zero initial backoff
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
	}

	client := createRetryableClient(t.Context(), config)

	// With zero initial backoff, jitter should also return zero
	backoff := client.Backoff(config.InitialBackoff, config.MaxBackoff, 0, nil)
	require.Equal(t, time.Duration(0), backoff)
}

// TestRetryableClient_ActualRetryBehavior tests the retry behavior in practice
func TestRetryableClient_ActualRetryBehavior(t *testing.T) {
	t.Parallel()
	var requestCount int32
	var retryDelays []time.Duration
	var retryTimes []time.Time

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		retryTimes = append(retryTimes, time.Now())

		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer server.Close()

	config := RetryConfig{
		MaxAttempts:       3,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        500 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}

	client := createRetryableClient(t.Context(), config)
	client.HTTPClient = server.Client()

	startTime := time.Now()
	req, err := retryablehttp.NewRequestWithContext(t.Context(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Should have made 3 requests (initial + 2 retries)
	require.Equal(t, int32(3), atomic.LoadInt32(&requestCount))
	require.Len(t, retryTimes, 3)

	// Calculate actual delays between requests
	for i := 1; i < len(retryTimes); i++ {
		delay := retryTimes[i].Sub(retryTimes[i-1])
		retryDelays = append(retryDelays, delay)
	}

	// Verify we had some delays due to backoff (but jittered, so variable)
	require.Len(t, retryDelays, 2)

	// First retry delay should be jittered version of 50ms (0-50ms range)
	// But in practice, with network overhead, it might be slightly higher
	require.Greater(t, retryDelays[0], time.Duration(0))
	require.Less(t, retryDelays[0], 200*time.Millisecond) // Allow some overhead

	// Second retry delay should be jittered version of 100ms (0-100ms range)
	require.Greater(t, retryDelays[1], time.Duration(0))
	require.Less(t, retryDelays[1], 300*time.Millisecond) // Allow some overhead

	totalTime := time.Since(startTime)
	t.Logf("Total time: %v, Retry delays: %v", totalTime, retryDelays)
}
