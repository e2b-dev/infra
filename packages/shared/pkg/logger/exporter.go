package logger

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
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

func (h *HTTPWriter) Write(source []byte) (n int, err error) {
	h.wg.Add(1)

        // zap is reusing the buffer, so since we're processing it in a Go routine, we need to make a copy.
	p := make([]byte, len(source))
	copy(p, source)

	// Run in a goroutine to avoid blocking the main thread
	go func() {
		defer h.wg.Done()

		start := 0
		for i, b := range p {
			if b == '\n' {
				if start < i { // Ignore empty lines
					line := p[start:i]
					if err := h.sendLogLine(line); err != nil {
						log.Printf("Failed to send a log line: %s\n", line)
						return
					}
				}
				start = i + 1 // Move start to the next line
			}
		}

		// Handle the last line if there’s no trailing newline
		if start < len(p) {
			line := p[start:]
			if err := h.sendLogLine(line); err != nil {
				log.Printf("Failed to send a log line: %s\n", line)
				return
			}
		}
	}()

	return len(p), nil
}

func (h *HTTPWriter) Sync() error {
	h.wg.Wait()
	return nil
}

// sendLogLine handles sending ONE log line as an HTTP request
func (h *HTTPWriter) sendLogLine(line []byte) error {
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
