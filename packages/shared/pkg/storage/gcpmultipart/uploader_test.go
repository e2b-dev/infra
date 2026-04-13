package gcpmultipart

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

func createTestUploader(t *testing.T, handler http.HandlerFunc, retryConfigs ...retryConfig) *Uploader {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := retryConfig{maxAttempts: 10, initialBackoff: 10 * time.Millisecond, maxBackoff: 10 * time.Second, multiplier: 2}
	if len(retryConfigs) > 0 {
		cfg = retryConfigs[0]
	}

	client := &retryablehttp.Client{
		RetryMax:     cfg.maxAttempts - 1,
		RetryWaitMin: cfg.initialBackoff,
		RetryWaitMax: cfg.maxBackoff,
		CheckRetry:   retryablehttp.DefaultRetryPolicy,
		HTTPClient:   server.Client(),
		Backoff:      httpClient.Backoff,
	}

	return &Uploader{
		token:   testToken,
		baseURL: server.URL + "/" + testObjectName,
		client:  client,
	}
}

type retryConfig struct {
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	multiplier     float64
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

		response := xmlInitiateResponse{
			UploadID: expectedUploadID,
		}

		xmlData, _ := xml.Marshal(response)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		w.Write(xmlData)
	})

	uploader := createTestUploader(t, handler)
	uploadID, err := uploader.initiate(t.Context())

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

	uploader := createTestUploader(t, handler)
	etag, err := uploader.putPart(t.Context(), "test-upload-id", 1, testData)

	require.NoError(t, err)
	require.Equal(t, expectedETag, etag)
}

func TestMultipartUploader_UploadPart_MissingETag(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Don't set ETag header
		w.WriteHeader(http.StatusOK)
	})

	uploader := createTestUploader(t, handler)
	etag, err := uploader.putPart(t.Context(), "test-upload-id", 1, []byte("test"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "no ETag for part 1")
	require.Empty(t, etag)
}

func TestMultipartUploader_CompleteUpload_Success(t *testing.T) {
	t.Parallel()
	parts := []xmlPart{
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

		var completeReq xmlCompleteRequest
		err = xml.Unmarshal(body, &completeReq)
		assert.NoError(t, err)
		assert.Len(t, completeReq.Parts, 2)
		assert.Equal(t, 1, completeReq.Parts[0].PartNumber)
		assert.Equal(t, `"etag1"`, completeReq.Parts[0].ETag)

		w.WriteHeader(http.StatusOK)
	})

	uploader := createTestUploader(t, handler)
	err := uploader.complete(t.Context(), "test-upload-id", parts)
	require.NoError(t, err)
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return data
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
	var initiateCount, putPartCount, completeCount int32
	receivedParts := sync.Map{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			// Initiate upload
			atomic.AddInt32(&initiateCount, 1)
			uploadID = "test-upload-id-123"
			response := xmlInitiateResponse{
				UploadID: uploadID,
			}
			xmlData, _ := xml.Marshal(response)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			// Upload part
			partNum := atomic.AddInt32(&putPartCount, 1)
			body, _ := io.ReadAll(r.Body)
			receivedParts.Store(int(partNum), string(body))

			w.Header().Set("ETag", fmt.Sprintf(`"etag%d"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			// Complete upload
			atomic.AddInt32(&completeCount, 1)
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestUploader(t, handler)
	_, err = uploader.Upload(t.Context(), readTestFile(t, testFile), 2)
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&initiateCount))
	require.Equal(t, int32(1), atomic.LoadInt32(&completeCount))
	require.Positive(t, atomic.LoadInt32(&putPartCount))

	// Verify all parts were uploaded and content matches
	var reconstructed strings.Builder
	for i := 1; i <= int(atomic.LoadInt32(&putPartCount)); i++ {
		if part, ok := receivedParts.Load(i); ok {
			reconstructed.WriteString(part.(string))
		}
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

		response := xmlInitiateResponse{
			UploadID: expectedUploadID,
		}
		xmlData, _ := xml.Marshal(response)
		w.WriteHeader(http.StatusOK)
		w.Write(xmlData)
	})

	uploader := createTestUploader(t, handler, retryConfig{maxAttempts: 3, initialBackoff: 10 * time.Millisecond, maxBackoff: 1 * time.Second, multiplier: 2})
	uploadID, err := uploader.initiate(t.Context())

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
			response := xmlInitiateResponse{
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

	uploader := createTestUploader(t, handler)

	// Use high concurrency to stress test
	_, err = uploader.Upload(t.Context(), readTestFile(t, testFile), 50)
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
			response := xmlInitiateResponse{
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

	uploader := createTestUploader(t, handler, retryConfig{maxAttempts: 10, initialBackoff: 1 * time.Millisecond, maxBackoff: 100 * time.Millisecond, multiplier: 2})
	_, err = uploader.Upload(t.Context(), readTestFile(t, testFile), 10)
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
			response := xmlInitiateResponse{
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

	uploader := createTestUploader(t, handler, retryConfig{maxAttempts: maxAttempts, initialBackoff: 5 * time.Millisecond, maxBackoff: 50 * time.Millisecond, multiplier: 2})
	_, err = uploader.Upload(t.Context(), readTestFile(t, testFile), 5)
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
			response := xmlInitiateResponse{
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

	uploader := createTestUploader(t, handler)
	_, err = uploader.Upload(t.Context(), readTestFile(t, emptyFile), 5)
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
			response := xmlInitiateResponse{
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

	uploader := createTestUploader(t, handler)
	_, err = uploader.Upload(t.Context(), readTestFile(t, smallFile), 10)
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
	count, err := io.Copy(file, newRepeatReader('a', ChunkSize*totalChunks))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, int64(ChunkSize*totalChunks))
	err = file.Close()
	require.NoError(t, err)

	var activeConcurrency atomic.Int32
	var maxObservedConcurrency atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			response := xmlInitiateResponse{
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

	uploader := createTestUploader(t, handler)

	// Try with extremely high concurrency
	_, err = uploader.Upload(t.Context(), readTestFile(t, testFile), 1000)
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
	testContent := strings.Repeat("x", ChunkSize*2)
	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err)

	var partSizes []int
	var partSizesMu sync.Mutex

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == uploadsPath:
			response := xmlInitiateResponse{
				UploadID: "boundary-upload-id",
			}
			xmlData, _ := xml.Marshal(response)
			w.WriteHeader(http.StatusOK)
			w.Write(xmlData)

		case strings.Contains(r.URL.RawQuery, "partNumber"):
			body, _ := io.ReadAll(r.Body)
			partSizesMu.Lock()
			partSizes = append(partSizes, len(body))
			partSizesMu.Unlock()

			partNum := strings.Split(strings.Split(r.URL.RawQuery, "partNumber=")[1], "&")[0]
			w.Header().Set("ETag", fmt.Sprintf(`"boundary-etag-%s"`, partNum))
			w.WriteHeader(http.StatusOK)

		case strings.Contains(r.URL.RawQuery, "uploadId"):
			w.WriteHeader(http.StatusOK)
		}
	})

	uploader := createTestUploader(t, handler)
	_, err = uploader.Upload(t.Context(), readTestFile(t, testFile), 5)
	require.NoError(t, err)

	// Should have exactly 2 parts, each of ChunkSize
	require.Len(t, partSizes, 2)
	require.Equal(t, ChunkSize, partSizes[0])
	require.Equal(t, ChunkSize, partSizes[1])
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
			response := xmlInitiateResponse{
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

	uploader := createTestUploader(t, handler, retryConfig{maxAttempts: 5, initialBackoff: 1 * time.Millisecond, maxBackoff: 10 * time.Millisecond, multiplier: 2})
	_, err = uploader.Upload(t.Context(), readTestFile(t, testFile), 20)
	require.NoError(t, err)

	t.Logf("Total HTTP requests made: %d", atomic.LoadInt32(&totalRequests))

	// Verify that retries happened correctly under concurrent conditions
	retryAttempts.Range(func(key, value any) bool {
		attempts := atomic.LoadInt32(value.(*int32))
		require.GreaterOrEqual(t, attempts, int32(3), "Part %s should have at least 3 attempts", key)

		return true
	})
}
