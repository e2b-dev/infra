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
			zap.L().Debug(fmt.Sprintf("Request failed (attempt %d/%d), retrying in %v: %v",
				attempt, config.MaxAttempts, backoff, lastErr))
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

type UploadResult struct {
	PartNumber int
	ETag       string
	Error      error
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

func (m *MultipartUploader) UploadFileInParallel(filePath string, maxConcurrency int) error {
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

	zap.L().Debug(fmt.Sprintf("File size: %d bytes, uploading in %d parts of %d bytes each\n",
		fileSize, numParts, ChunkSize))

	// Initiate multipart upload
	zap.L().Debug("Initiating multipart upload...")
	uploadID, err := m.InitiateUpload()
	if err != nil {
		return fmt.Errorf("failed to initiate upload: %v", err)
	}
	zap.L().Debug(fmt.Sprintf("Upload ID: %s\n", uploadID))

	// Create channels for work distribution and results
	jobs := make(chan int, numParts)
	results := make(chan UploadResult, numParts)

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for partNumber := range jobs {
				// Read chunk from file
				offset := int64(partNumber-1) * ChunkSize
				chunkSize := ChunkSize
				if offset+int64(chunkSize) > fileSize {
					chunkSize = int(fileSize - offset)
				}

				chunk := make([]byte, chunkSize)
				_, err := file.ReadAt(chunk, offset)
				if err != nil {
					results <- UploadResult{PartNumber: partNumber, Error: err}
					continue
				}

				// Upload part
				zap.L().Debug(fmt.Sprintf("Uploading part %d/%d (size: %d bytes)\n", partNumber, numParts, len(chunk)))
				etag, err := m.UploadPart(uploadID, partNumber, chunk)
				results <- UploadResult{
					PartNumber: partNumber,
					ETag:       etag,
					Error:      err,
				}
			}
		}()
	}

	// Send jobs
	go func() {
		for i := 1; i <= numParts; i++ {
			jobs <- i
		}
		close(jobs)
	}()

	// Wait for workers to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var parts []Part
	var uploadErrors []error

	for result := range results {
		if result.Error != nil {
			uploadErrors = append(uploadErrors, fmt.Errorf("part %d: %v", result.PartNumber, result.Error))
		} else {
			parts = append(parts, Part{
				PartNumber: result.PartNumber,
				ETag:       result.ETag,
			})
			zap.L().Debug(fmt.Sprintf("Part %d uploaded successfully (ETag: %s)\n", result.PartNumber, result.ETag))
		}
	}

	// Check for errors
	if len(uploadErrors) > 0 {
		return fmt.Errorf("upload errors: %v", uploadErrors)
	}

	// Complete the upload
	fmt.Println("Completing multipart upload...")
	if err := m.CompleteUpload(uploadID, parts); err != nil {
		return fmt.Errorf("failed to complete upload: %v", err)
	}

	fmt.Println("Upload completed successfully!")
	return nil
}
