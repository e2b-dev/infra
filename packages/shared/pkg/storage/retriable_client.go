package storage

import (
	"context"
	"math/rand"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

	// add otel instrumentation
	originalTransport := client.HTTPClient.Transport
	client.HTTPClient.Transport = otelhttp.NewTransport(originalTransport)

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
