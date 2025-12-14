package storage

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"go.uber.org/zap"
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

func MultipartUploadFile(ctx context.Context, filePath string, u MultipartUploader, maxConcurrency int, compression CompressionType) error {
	// Open input file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	if compression == CompressionNone {
		// Use original simple implementation - upload file as-is
		return uploadFileUncompressed(ctx, file, u, maxConcurrency)
	}

	// Use compressed implementation
	_, err = MultipartCompressUploadFile(ctx, file, u, maxConcurrency, compression)

	return err
}

// uploadFileUncompressed uploads the file without compression (original implementation)
func uploadFileUncompressed(ctx context.Context, file *os.File, u MultipartUploader, maxConcurrency int) error {
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
