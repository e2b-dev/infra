package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap/zapcore"
)

var (
	logWriterMeter      = otel.Meter("github.com/e2b-dev/infra/packages/shared/pkg/logger")
	logWriterWriteCount = mustLogWriterCounter(
		"log_writer_write_count",
		"Number of log writer HTTP attempts by route and result",
	)
)

const maxStaticShadowWrites = 1024

func mustLogWriterCounter(name, description string) metric.Int64Counter {
	counter, err := logWriterMeter.Int64Counter(name, metric.WithDescription(description))
	if err != nil {
		return nil
	}

	return counter
}

type HTTPWriter struct {
	ctx            context.Context //nolint:containedctx // todo: fix the interface so this can be removed
	url            string
	httpClient     *http.Client
	shadowURL      string
	shadowInflight atomic.Int64

	wgLock sync.Mutex
	wg     *sync.WaitGroup
}

func NewHTTPWriter(ctx context.Context, endpoint string) zapcore.WriteSyncer {
	return &HTTPWriter{
		ctx: ctx,
		url: endpoint,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		wgLock: sync.Mutex{},
		wg:     &sync.WaitGroup{},
	}
}

// NewDualHTTPWriter writes synchronously to primary and asynchronously to one
// fixed, bounded best-effort shadow destination. The shadow never affects the
// primary result and is not dynamically resolved.
func NewDualHTTPWriter(ctx context.Context, primary, shadow string) zapcore.WriteSyncer {
	writer := NewHTTPWriter(ctx, primary).(*HTTPWriter)
	writer.shadowURL = shadow

	return writer
}

func (h *HTTPWriter) Write(source []byte) (n int, err error) {
	h.wgLock.Lock()
	// Capture the current WaitGroup to match Add/Done calls
	wg := h.wg
	wg.Add(1)
	h.wgLock.Unlock()

	// zap is reusing the buffer, so since we're processing it in a Go routine, we need to make a copy.
	p := make([]byte, len(source))
	copy(p, source)

	// Run in a goroutine to avoid blocking the main thread
	go func() {
		defer wg.Done()

		start := 0
		for i, b := range p {
			if b == '\n' {
				if start < i { // Ignore empty lines
					line := p[start:i]
					if err := h.routeLogLine(line); err != nil {
						// Never log the line itself: it is customer log content.
						log.Printf("error routing log line (%d bytes): %v\n", len(line), err)

						return
					}
				}
				start = i + 1 // Move start to the next line
			}
		}

		// Handle the last line if there’s no trailing newline
		if start < len(p) {
			line := p[start:]
			if err := h.routeLogLine(line); err != nil {
				// Never log the line itself: it is customer log content.
				log.Printf("error routing log line (%d bytes): %v\n", len(line), err)

				return
			}
		}
	}()

	return len(p), nil
}

func (h *HTTPWriter) Sync() error {
	h.wgLock.Lock()
	// Capture the current WaitGroup for Wait() and replace the WaitGroup with a new one
	// Should fix: WaitGroup is reused before previous Wait has returned
	wg := h.wg
	h.wg = &sync.WaitGroup{}
	h.wgLock.Unlock()

	wg.Wait()

	return nil
}

// routeLogLine sends ONE log line to the fixed endpoint.
func (h *HTTPWriter) routeLogLine(line []byte) error {
	if h.shadowURL != "" && h.tryAcquireShadow() {
		shadowLine := append([]byte(nil), line...)
		go func() {
			defer h.shadowInflight.Add(-1)
			if err := h.sendLogLine(h.ctx, h.shadowURL, shadowLine); err != nil {
				log.Printf("error sending log shadow: %v\n", err)
			}
		}()
	}

	if err := h.sendLogLine(h.ctx, h.url, line); err != nil {
		recordLogWriterWrite(h.ctx, "primary", "failure", "send_error")

		return err
	}
	recordLogWriterWrite(h.ctx, "primary", "success", "")

	return nil
}

func (h *HTTPWriter) tryAcquireShadow() bool {
	for {
		current := h.shadowInflight.Load()
		if current >= maxStaticShadowWrites {
			return false
		}
		if h.shadowInflight.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func recordLogWriterWrite(ctx context.Context, route, result, reason string) {
	if logWriterWriteCount == nil {
		return
	}

	logWriterWriteCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("route", route),
		attribute.String("result", result),
		attribute.String("reason", reason),
	))
}

// sendLogLine handles sending ONE log line as an HTTP request to url.
func (h *HTTPWriter) sendLogLine(ctx context.Context, url string, line []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(line))
	if err != nil {
		return fmt.Errorf("error sending logs: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := h.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("error sending logs: %w", err)
	}
	defer response.Body.Close()

	// Always drain so the transport can reuse the connection; the body itself
	// is never surfaced in the returned error (it may echo request content).
	drainErr := drainResponseBody(response.Body)

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		statusErr := fmt.Errorf("error sending logs: unexpected HTTP status %d", response.StatusCode)

		return errors.Join(statusErr, drainErr)
	}

	return drainErr
}

func drainResponseBody(body io.Reader) error {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return fmt.Errorf("error draining response body: %w", err)
	}

	return nil
}
