package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/SaveTheRbtz/fastcdc-go"
	seekable "github.com/SaveTheRbtz/zstd-seekable-format-go/pkg"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/klauspost/compress/zstd"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	gcpMultipartUploadChunkSize = 50 * 1024 * 1024 // 50MB chunks
	targetFrameSize             = 4 * 1024 * 1024  // 4MiB target frame size
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

func createRetryableClient(ctx context.Context, config RetryConfig) *retryablehttp.Client {
	client := retryablehttp.NewClient()

	client.RetryMax = config.MaxAttempts - 1 // go-retryablehttp counts retries, not total attempts
	client.RetryWaitMin = config.InitialBackoff
	client.RetryWaitMax = config.MaxBackoff

	// Custom backoff function with full jitter to avoid thundering herd
	client.Backoff = func(start, maxBackoff time.Duration, attemptNum int, _ *http.Response) time.Duration {
		// Calculate exponential backoff
		backoff := start
		for range attemptNum {
			backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
			if backoff > maxBackoff {
				backoff = maxBackoff

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
	client.Logger = &leveledLogger{
		logger: logger.L().Detach(ctx),
	}

	return client
}

// zapLogger adapts zap.Logger to retryablehttp.LeveledLogger interface
var _ retryablehttp.LeveledLogger = &leveledLogger{}

type leveledLogger struct {
	logger *zap.Logger
}

func (z *leveledLogger) Error(msg string, keysAndValues ...any) {
	z.logger.Error(msg, zap.Any("details", keysAndValues))
}

func (z *leveledLogger) Info(msg string, keysAndValues ...any) {
	z.logger.Info(msg, zap.Any("details", keysAndValues))
}

func (z *leveledLogger) Debug(string, ...any) {
	// Ignore debug logs
}

func (z *leveledLogger) Warn(msg string, keysAndValues ...any) {
	z.logger.Warn(msg, zap.Any("details", keysAndValues))
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
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	return &MultipartUploader{
		bucketName:  bucketName,
		objectName:  objectName,
		token:       token.AccessToken,
		client:      createRetryableClient(ctx, retryConfig),
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf("failed to initiate upload (status %d): %s", resp.StatusCode, string(body))
	}

	var result InitiateMultipartUploadResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse initiate response: %w", err)
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

	if resp.StatusCode != http.StatusOK {
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
		return fmt.Errorf("failed to marshal complete request: %w", err)
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("failed to complete upload (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (m *MultipartUploader) UploadFileInParallel(ctx context.Context, filePath string, maxConcurrency int, compression CompressionType) error {
	// Open input file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	if compression == CompressionNone {
		// Use original simple implementation - upload file as-is
		return m.uploadFileUncompressed(ctx, file, maxConcurrency)
	}

	// Use compressed implementation
	return m.uploadFileCompressed(ctx, file, maxConcurrency)
}

// uploadFileUncompressed uploads the file without compression (original implementation)
func (m *MultipartUploader) uploadFileUncompressed(ctx context.Context, file *os.File, maxConcurrency int) error {
	// Get file size
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file stats: %w", err)
	}
	fileSize := fileInfo.Size()

	// Calculate number of parts
	numParts := int((fileSize + gcpMultipartUploadChunkSize - 1) / gcpMultipartUploadChunkSize)
	if numParts == 0 {
		numParts = 1
	}

	// Initiate multipart upload
	uploadID, err := m.InitiateUpload()
	if err != nil {
		return fmt.Errorf("failed to initiate upload: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

	// Thread-safe slice to collect parts
	var partsMu sync.Mutex
	parts := make([]Part, numParts)

	// Upload each part concurrently
	for partNumber := 1; partNumber <= numParts; partNumber++ {
		partNum := partNumber // Capture for closure
		g.Go(func() error {
			// Check if context was cancelled
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Read chunk from file
			offset := int64(partNum-1) * gcpMultipartUploadChunkSize
			chunkSize := gcpMultipartUploadChunkSize
			if offset+int64(chunkSize) > fileSize {
				chunkSize = int(fileSize - offset)
			}

			// Read chunk from file
			chunk := make([]byte, chunkSize)
			_, err := file.ReadAt(chunk, offset)
			if err != nil {
				return fmt.Errorf("failed to read chunk: %w", err)
			}

			// Upload part
			etag, err := m.UploadPart(uploadID, partNum, chunk)
			if err != nil {
				return fmt.Errorf("failed to upload part %d: %w", partNum, err)
			}

			// Store result thread-safely
			partsMu.Lock()
			parts[partNum-1] = Part{
				PartNumber: partNum,
				ETag:       etag,
			}
			partsMu.Unlock()

			return nil
		})
	}

	// Wait for all parts to complete or first error
	if err := g.Wait(); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	return m.CompleteUpload(uploadID, parts)
}

// uploadFileCompressed uploads the file with zstd seekable compression
func (m *MultipartUploader) uploadFileCompressed(ctx context.Context, file *os.File, maxConcurrency int) error {
	// Get file size for comparison
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file stats: %w", err)
	}
	originalSize := stat.Size()
	fmt.Fprintf(os.Stderr, "[DEBUG] Original file size: %d bytes\n", originalSize)

	// Create a buffer to hold the compressed data
	var compressedBuf bytes.Buffer

	// Create ZSTD encoder - hardcode best compression like main.go
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(1)))
	if err != nil {
		return fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	defer enc.Close()
	fmt.Fprintf(os.Stderr, "[DEBUG] Created ZSTD encoder with level 11\n")

	// Create seekable writer
	w, err := seekable.NewWriter(&compressedBuf, enc, seekable.WithWLogger(zap.L()))
	if err != nil {
		return fmt.Errorf("failed to create seekable writer: %w", err)
	}
	defer w.Close()
	fmt.Fprintf(os.Stderr, "[DEBUG] Created seekable writer\n")

	// Create FastCDC chunker - hardcode params like main.go
	chunker, err := fastcdc.NewChunker(
		file,
		fastcdc.Options{
			MinSize:     4 * 1024,        // 4KB
			AverageSize: 1024 * 1024,     // 1MB
			MaxSize:     4 * 1024 * 1024, // 4MB
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create chunker: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] Created FastCDC chunker (128KB/1MB/8MB)\n")

	var totalFrames int
	var totalFrameBytes int64

	// Frame source function - exactly like main.go
	frameSource := func() ([]byte, error) {
		var frameBuffer bytes.Buffer

		// Accumulate chunks until we reach target frame size
		for frameBuffer.Len() < targetFrameSize {
			chunk, err := chunker.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Return any remaining data in buffer
					if frameBuffer.Len() > 0 {
						totalFrames++
						totalFrameBytes += int64(frameBuffer.Len())
						fmt.Fprintf(os.Stderr, "[DEBUG] Final frame %d: %d bytes\n", totalFrames, frameBuffer.Len())
						return frameBuffer.Bytes(), nil
					}
					return nil, nil
				}
				return nil, err
			}

			// Add chunk data to frame buffer
			frameBuffer.Write(chunk.Data)
		}

		totalFrames++
		totalFrameBytes += int64(frameBuffer.Len())
		fmt.Fprintf(os.Stderr, "[DEBUG] Frame %d: %d bytes\n", totalFrames, frameBuffer.Len())
		return frameBuffer.Bytes(), nil
	}

	// Use WriteMany to compress data in frames - exactly like main.go
	err = w.WriteMany(ctx, frameSource)
	if err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] WriteMany completed. Total frames: %d, total frame bytes: %d\n", totalFrames, totalFrameBytes)

	w.Close()

	// Now upload the compressed data using multipart
	compressedData := compressedBuf.Bytes()
	compressedSize := int64(len(compressedData))
	fmt.Fprintf(os.Stderr, "[DEBUG] Compressed size: %d bytes\n", compressedSize)
	fmt.Fprintf(os.Stderr, "[DEBUG] Compression ratio: %.2f:1\n", float64(originalSize)/float64(compressedSize))

	// Calculate number of parts based on compressed size
	numParts := int((compressedSize + gcpMultipartUploadChunkSize - 1) / gcpMultipartUploadChunkSize)
	if numParts == 0 {
		numParts = 1 // Always upload at least 1 part, even for empty files
	}

	// Initiate multipart upload
	uploadID, err := m.InitiateUpload()
	if err != nil {
		return fmt.Errorf("failed to initiate upload: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

	// Thread-safe slice to collect parts
	var partsMu sync.Mutex
	parts := make([]Part, numParts)

	// Upload each part concurrently
	for partNumber := 1; partNumber <= numParts; partNumber++ {
		partNum := partNumber // Capture for closure
		g.Go(func() error {
			// Calculate chunk boundaries
			offset := int64(partNum-1) * gcpMultipartUploadChunkSize
			chunkSize := gcpMultipartUploadChunkSize
			if offset+int64(chunkSize) > compressedSize {
				chunkSize = int(compressedSize - offset)
			}

			// Extract chunk from compressed data
			chunk := compressedData[offset : offset+int64(chunkSize)]

			// Upload part
			etag, err := m.UploadPart(uploadID, partNum, chunk)
			if err != nil {
				return fmt.Errorf("failed to upload part %d: %w", partNum, err)
			}

			// Store result thread-safely
			partsMu.Lock()
			parts[partNum-1] = Part{
				PartNumber: partNum,
				ETag:       etag,
			}
			partsMu.Unlock()

			return nil
		})
	}

	// Wait for all parts to complete or first error
	if err := g.Wait(); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	return m.CompleteUpload(uploadID, parts)
}
