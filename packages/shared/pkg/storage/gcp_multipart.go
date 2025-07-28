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
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

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

// retryRequest performs an HTTP request with exponential backoff retry logic
func retryRequest(client *http.Client, req *http.Request, config RetryConfig) (*http.Response, error) {
	var lastErr error
	backoff := config.InitialBackoff

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Clone the request for retry (in case body was consumed)
		clonedReq := req.Clone(req.Context())
		if req.Body != nil {
			// Reset body for retry attempts
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("failed to get request body for retry: %v", err)
				}
				clonedReq.Body = body
			}
		}

		resp, err := client.Do(clonedReq)
		if err == nil && resp.StatusCode < 500 {
			// Success or client error (4xx) - don't retry
			return resp, nil
		}

		// Store the error for potential return
		if err != nil {
			lastErr = err
		} else {
			// Server error (5xx)
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("server error (status %d): %s", resp.StatusCode, string(body))
		}

		// Don't sleep on the last attempt
		if attempt < config.MaxAttempts {
			zap.L().Warn("Request failed",
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", config.MaxAttempts),
				zap.Duration("backoff", backoff),
				zap.Error(lastErr),
			)
			time.Sleep(backoff)

			// Calculate next backoff
			backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
			if backoff > config.MaxBackoff {
				backoff = config.MaxBackoff
			}
		}
	}

	return nil, fmt.Errorf("request failed after %d attempts: %v", config.MaxAttempts, lastErr)
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
	client      *http.Client
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
		client:      &http.Client{},
		retryConfig: retryConfig,
		baseURL:     fmt.Sprintf("https://%s.storage.googleapis.com", bucketName),
	}, nil
}

func (m *MultipartUploader) InitiateUpload() (string, error) {
	url := fmt.Sprintf("%s/%s?uploads", m.baseURL, m.objectName)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Length", "0")
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := retryRequest(m.client, req, m.retryConfig)
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

	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}

	// Set up GetBody for retries
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
	req.Header.Set("Content-MD5", md5Sum)

	resp, err := retryRequest(m.client, req, m.retryConfig)
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

	req, err := http.NewRequest("POST", url, bytes.NewReader(xmlData))
	if err != nil {
		return err
	}

	// Set up GetBody for retries
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(xmlData)), nil
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(xmlData)))

	resp, err := retryRequest(m.client, req, m.retryConfig)
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
		partNumber := partNumber // Capture loop variable
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
