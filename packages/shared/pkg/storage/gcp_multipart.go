package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/errgroup"
)

const (
	ChunkSize = 50 * 1024 * 1024 // 50MB chunks
)

// RetryConfig holds the configuration for retry logic
type RetryConfig struct {
	MaxAttempts       int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
}

// DefaultRetryConfig returns the default retry configuration matching storage_google.go
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:       googleMaxAttempts,
		InitialBackoff:    googleInitialBackoff,
		MaxBackoff:        googleMaxBackoff,
		BackoffMultiplier: googleBackoffMultiplier,
	}
}

func createRetryableClient(config RetryConfig) *retryablehttp.Client {
	client := retryablehttp.NewClient()

	client.RetryMax = config.MaxAttempts - 1 // go-retryablehttp counts retries, not total attempts
	client.RetryWaitMin = config.InitialBackoff
	client.RetryWaitMax = config.MaxBackoff

	// Custom backoff function with full jitter to avoid thundering herd
	client.Backoff = func(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
		// Calculate exponential backoff
		backoff := min
		for range attemptNum {
			backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
			if backoff > max {
				backoff = max
				break
			}
		}

		// Apply full jitter: random(0, backoff)
		// This implements the "full jitter" strategy recommended by AWS:
		// https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
		// Benefits:
		// - Spreads retry attempts across time to avoid thundering herd
		// - Reduces peak load on servers during outages
		// - Improves overall system stability under high retry scenarios
		if backoff > 0 {
			return time.Duration(rand.Int63n(int64(backoff)))
		}
		return backoff
	}

	// Use zap logger
	client.Logger = &zapLogger{}

	return client
}

// zapLogger adapts zap.Logger to retryablehttp.LeveledLogger interface
type zapLogger struct{}

func (z *zapLogger) Error(msg string, keysAndValues ...any) {
	zap.L().Error(msg, zap.Any("details", keysAndValues))
}

func (z *zapLogger) Info(msg string, keysAndValues ...any) {
	zap.L().Info(msg, zap.Any("details", keysAndValues))
}

func (z *zapLogger) Debug(msg string, keysAndValues ...any) {
	// Ignore debug logs
}

func (z *zapLogger) Warn(msg string, keysAndValues ...any) {
	zap.L().Warn(msg, zap.Any("details", keysAndValues))
}

type InitiateMultipartUploadResult struct {
	Bucket   string `xml:"Bucket"`
	Key      string `xml:"Key"`
	UploadID string `xml:"UploadId"`
}

type CompleteMultipartUpload struct {
	XMLName string `xml:"CompleteMultipartUpload"`
	Parts   []Part `xml:"Part"`
}

type Part struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type MultipartUploader struct {
	bucketName  string
	objectName  string
	token       string
	client      *retryablehttp.Client
	retryConfig RetryConfig
	baseURL     string // Allow overriding for testing
}

func NewMultipartUploaderWithRetryConfig(ctx context.Context, bucketName, objectName string, retryConfig RetryConfig) (*MultipartUploader, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %v", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %v", err)
	}

	return &MultipartUploader{
		bucketName:  bucketName,
		objectName:  objectName,
		token:       token.AccessToken,
		client:      createRetryableClient(retryConfig),
		retryConfig: retryConfig,
		baseURL:     fmt.Sprintf("https://%s.storage.googleapis.com", bucketName),
	}, nil
}

func (m *MultipartUploader) InitiateUpload() (string, error) {
	url := fmt.Sprintf("%s/%s?uploads", m.baseURL, m.objectName)

	req, err := retryablehttp.NewRequest("POST", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Length", "0")
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to initiate upload (status %d): %s", resp.StatusCode, string(body))
	}

	var result InitiateMultipartUploadResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse initiate response: %v", err)
	}

	return result.UploadID, nil
}

func (m *MultipartUploader) UploadPart(uploadID string, partNumber int, data []byte) (string, error) {
	// Calculate MD5 for data integrity
	hasher := md5.New()
	hasher.Write(data)
	md5Sum := base64.StdEncoding.EncodeToString(hasher.Sum(nil))

	url := fmt.Sprintf("%s/%s?partNumber=%d&uploadId=%s",
		m.baseURL, m.objectName, partNumber, uploadID)

	req, err := retryablehttp.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
	req.Header.Set("Content-MD5", md5Sum)

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to upload part %d (status %d): %s", partNumber, resp.StatusCode, string(body))
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("no ETag returned for part %d", partNumber)
	}

	return etag, nil
}

func (m *MultipartUploader) CompleteUpload(uploadID string, parts []Part) error {
	// Sort parts by part number
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})

	completeReq := CompleteMultipartUpload{Parts: parts}
	xmlData, err := xml.Marshal(completeReq)
	if err != nil {
		return fmt.Errorf("failed to marshal complete request: %v", err)
	}

	url := fmt.Sprintf("%s/%s?uploadId=%s",
		m.baseURL, m.objectName, uploadID)

	req, err := retryablehttp.NewRequest("POST", url, bytes.NewReader(xmlData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(xmlData)))

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to complete upload (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (m *MultipartUploader) UploadFileInParallel(ctx context.Context, filePath string, maxConcurrency int) error {
	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	// Get file size
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %v", err)
	}
	fileSize := fileInfo.Size()

	// Calculate number of parts
	numParts := int(math.Ceil(float64(fileSize) / float64(ChunkSize)))
	if numParts == 0 {
		numParts = 1 // Always upload at least 1 part, even for empty files
	}

	// Initiate multipart upload
	uploadID, err := m.InitiateUpload()
	if err != nil {
		return fmt.Errorf("failed to initiate upload: %v", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency) // Limit concurrent goroutines

	// Thread-safe map to collect parts
	var partsMu sync.Mutex
	parts := make([]Part, numParts)

	// Upload each part concurrently
	for partNumber := 1; partNumber <= numParts; partNumber++ {
		g.Go(func() error {
			// Check if context was cancelled
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Read chunk from file
			offset := int64(partNumber-1) * ChunkSize
			chunkSize := ChunkSize
			if offset+int64(chunkSize) > fileSize {
				chunkSize = int(fileSize - offset)
			}

			chunk := make([]byte, chunkSize)
			_, err := file.ReadAt(chunk, offset)
			if err != nil {
				return fmt.Errorf("failed to read chunk for part %d: %v", partNumber, err)
			}

			// Upload part
			etag, err := m.UploadPart(uploadID, partNumber, chunk)
			if err != nil {
				return fmt.Errorf("failed to upload part %d: %v", partNumber, err)
			}

			// Store result thread-safely
			partsMu.Lock()
			parts[partNumber-1] = Part{
				PartNumber: partNumber,
				ETag:       etag,
			}
			partsMu.Unlock()

			return nil
		})
	}

	// Wait for all parts to complete or first error
	if err := g.Wait(); err != nil {
		return fmt.Errorf("upload failed: %v", err)
	}

	if err := m.CompleteUpload(uploadID, parts); err != nil {
		return fmt.Errorf("failed to complete upload: %v", err)
	}

	return nil
}
