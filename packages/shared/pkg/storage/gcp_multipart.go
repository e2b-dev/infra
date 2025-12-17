package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
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

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	gcpMultipartUploadPartSize = 50 * 1024 * 1024 // 50Mb parts
)

type MultipartUploader interface {
	InitiateUpload() (id string, err error)
	UploadPart(id string, partNumber int, data ...[]byte) (err error)
	CompleteUpload(id string) error
}

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

// MultipartUploadFile uploads the file without compression (original implementation)
func MultipartUploadFile(ctx context.Context, filePath string, u MultipartUploader, maxConcurrency int) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file size
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file stats: %w", err)
	}
	fileSize := fileInfo.Size()

	// Calculate number of parts
	numParts := int((fileSize + gcpMultipartUploadPartSize - 1) / gcpMultipartUploadPartSize)
	if numParts == 0 {
		numParts = 1
	}

	// Initiate multipart upload
	uploadID, err := u.InitiateUpload()
	if err != nil {
		return fmt.Errorf("failed to initiate upload: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

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
			offset := int64(partNum-1) * gcpMultipartUploadPartSize
			chunkSize := gcpMultipartUploadPartSize
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
			err = u.UploadPart(uploadID, partNum, chunk)
			if err != nil {
				return fmt.Errorf("failed to upload part %d: %w", partNum, err)
			}

			return nil
		})
	}

	// Wait for all parts to complete or first error
	if err := g.Wait(); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	return u.CompleteUpload(uploadID)
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

type multipartUploaderGCP struct {
	bucketName  string
	objectName  string
	token       string
	client      *retryablehttp.Client
	retryConfig RetryConfig
	baseURL     string // Allow overriding for testing
	etags       *sync.Map
}

var _ MultipartUploader = (*multipartUploaderGCP)(nil)

func NewGCPUploaderWithRetryConfig(ctx context.Context, bucketName, objectName string, retryConfig RetryConfig) (*multipartUploaderGCP, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	return &multipartUploaderGCP{
		etags:       &sync.Map{},
		bucketName:  bucketName,
		objectName:  objectName,
		token:       token.AccessToken,
		client:      createRetryableClient(ctx, retryConfig),
		retryConfig: retryConfig,
		baseURL:     fmt.Sprintf("https://%s.storage.googleapis.com", bucketName),
	}, nil
}

func (m *multipartUploaderGCP) InitiateUpload() (string, error) {
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

func (m *multipartUploaderGCP) UploadPart(uploadID string, partNumber int, dataList ...[]byte) error {
	// Calculate MD5 for data integrity
	hasher := md5.New()
	l := 0
	for _, data := range dataList {
		_, _ = hasher.Write(data)
		l += len(data)
	}
	md5Sum := base64.StdEncoding.EncodeToString(hasher.Sum(nil))

	url := fmt.Sprintf("%s/%s?partNumber=%d&uploadId=%s",
		m.baseURL, m.objectName, partNumber, uploadID)

	req, err := retryablehttp.NewRequest("PUT", url, newVectorReader(dataList))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", l))
	req.Header.Set("Content-MD5", md5Sum)

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("failed to upload part %d (status %d): %s", partNumber, resp.StatusCode, string(body))
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		return fmt.Errorf("no ETag returned for part %d", partNumber)
	}
	m.etags.Store(partNumber, etag)

	return nil
}

func (m *multipartUploaderGCP) CompleteUpload(uploadID string) error {
	// Collect parts
	parts := make([]Part, 0)
	m.etags.Range(func(key, value any) bool {
		partNumber, ok := key.(int)
		if !ok {
			return false
		}
		etag, ok := value.(string)
		if !ok {
			return false
		}
		parts = append(parts, Part{
			PartNumber: partNumber,
			ETag:       etag,
		})

		return true
	})

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
