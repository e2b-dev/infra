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
	ctx        context.Context //nolint:containedctx // todo: fix the interface so this can be removed
	url        string
	httpClient *http.Client

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
					if err := h.sendLogLine(line); err != nil {
						log.Printf("%v: %s\n", err, line)

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
				log.Printf("%v: %s\n", err, line)

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
