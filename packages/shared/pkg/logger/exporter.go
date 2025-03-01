package logger

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

type HTTPWriter struct {
	ctx        context.Context
	url        string
	httpClient *http.Client
	wg         sync.WaitGroup
}

func NewHTTPWriter(ctx context.Context, endpoint string) zapcore.WriteSyncer {
	return &HTTPWriter{
		ctx: ctx,
		url: endpoint,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		wg: sync.WaitGroup{},
	}
}

func NewBufferedHTTPWriter(ctx context.Context, endpoint string) zapcore.WriteSyncer {
	httpWriter := &zapcore.BufferedWriteSyncer{
		WS:            NewHTTPWriter(ctx, endpoint),
		Size:          256 * 1024, // 256 kB
		FlushInterval: 5 * time.Second,
	}

	go func() {
		select {
		case <-ctx.Done():
			if err := httpWriter.Stop(); err != nil {
				fmt.Printf("Error stopping HTTP writer: %v\n", err)
			}
		}
	}()

	return httpWriter
}

// Write sends the logs to the HTTP endpoint.
func (h *HTTPWriter) Write(p []byte) (n int, err error) {
	start := 0
	for i, b := range p {
		if b == '\n' {
			if start < i { // Ignore empty lines
				if err := h.sendLogLine(p[start:i]); err != nil {
					return start, err
				}
			}
			start = i + 1 // Move start to the next line
		}
	}

	// Handle last line if there’s no trailing newline
	if start < len(p) {
		if err := h.sendLogLine(p[start:]); err != nil {
			return start, err
		}
	}

	return len(p), nil
}

// Sync is required by zapcore.WriteSyncer.
func (h *HTTPWriter) Sync() error {
	h.wg.Wait()
	return nil
}

// sendLog handles sending the log line as an HTTP request
func (h *HTTPWriter) sendLogLine(line []byte) error {
	h.wg.Add(1)
	defer h.wg.Done()

	fmt.Fprintf(os.Stderr, "Sending log line: %s\n", line)

	request, err := http.NewRequestWithContext(h.ctx, http.MethodPost, h.url, bytes.NewReader(line))
	if err != nil {
		return fmt.Errorf("error sending logs: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := h.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("error sending logs: %w", err)
	}

	err = response.Body.Close()
	if err != nil {
		return fmt.Errorf("error closing response body: %w", err)
	}
	return nil
}
