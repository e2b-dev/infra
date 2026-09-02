package exporter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
)

const (
	ExporterTimeout = 10 * time.Second

	// Under Loki's 256 KiB max_line_size default.
	maxLogLineBytes  = 192 << 10
	maxBufferedBytes = 8 << 20

	logFloor = time.Minute
)

type HTTPExporter struct {
	client        http.Client
	logs          [][]byte
	bufferedBytes int
	mmdsOpts      atomic.Pointer[host.MMDSOpts]

	jsonErrLog   *rateLimitedLogger
	sendErrLog   *rateLimitedLogger
	oversizedLog *rateLimitedLogger

	// Concurrency coordination
	triggers  chan struct{}
	logLock   sync.Mutex
	startOnce sync.Once
}

func NewHTTPLogsExporter(ctx context.Context, mmdsChan <-chan *host.MMDSOpts) *HTTPExporter {
	exporter := &HTTPExporter{
		client: http.Client{
			Timeout:   ExporterTimeout,
			Transport: &http.Transport{DisableKeepAlives: true},
		},
		triggers:     make(chan struct{}, 1),
		jsonErrLog:   newRateLimitedLogger("error adding instance logging options to JSON: %v"),
		sendErrLog:   newRateLimitedLogger("error sending instance logs: %+v"),
		oversizedLog: newRateLimitedLogger("dropped log line exceeding %d bytes"),
	}

	go exporter.listenForMMDSOptsAndStart(ctx, mmdsChan)

	return exporter
}

func (w *HTTPExporter) sendInstanceLogs(ctx context.Context, logs []byte, address string) error {
	if address == "" {
		return nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewBuffer(logs))
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := w.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("collector returned %s", response.Status)
	}

	return nil
}

// FlushAndPurge synchronously sends the logs buffered when the call begins,
// then discards anything still buffered. Sending is best-effort: the first
// error is returned to the caller, while the purge always runs.
func (w *HTTPExporter) FlushAndPurge(ctx context.Context) error {
	logs := w.getAllLogs()
	defer w.purgeLogs()

	opts := w.mmdsOpts.Load()
	if opts == nil {
		return nil
	}

	var flushErr error
	for _, logLine := range logs {
		if err := ctx.Err(); err != nil {
			if flushErr == nil {
				flushErr = err
			}

			break
		}

		logLineWithOpts, err := opts.AddOptsToJSON(logLine)
		if err != nil {
			if flushErr == nil {
				flushErr = fmt.Errorf("add instance logging options to JSON: %w", err)
			}

			continue
		}

		if err := w.sendInstanceLogs(ctx, logLineWithOpts, opts.LogsCollectorAddress); err != nil {
			if flushErr == nil {
				flushErr = fmt.Errorf("send instance logs: %w", err)
			}
			if ctx.Err() != nil {
				break
			}
		}
	}

	return flushErr
}

func (w *HTTPExporter) listenForMMDSOptsAndStart(ctx context.Context, mmdsChan <-chan *host.MMDSOpts) {
	for {
		select {
		case <-ctx.Done():
			return
		case mmdsOpts, ok := <-mmdsChan:
			if !ok {
				return
			}

			w.mmdsOpts.Store(mmdsOpts)

			w.startOnce.Do(func() {
				go w.start(ctx)
			})
		}
	}
}

func (w *HTTPExporter) start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.triggers:
		}

		logs := w.getAllLogs()

		if len(logs) == 0 {
			continue
		}

		opts := w.mmdsOpts.Load()
		if opts == nil {
			continue
		}

		for _, logLine := range logs {
			logLineWithOpts, err := opts.AddOptsToJSON(logLine)
			if err != nil {
				w.jsonErrLog.log(err)

				continue
			}

			if err := w.sendInstanceLogs(ctx, logLineWithOpts, opts.LogsCollectorAddress); err != nil {
				w.sendErrLog.log(err)

				continue
			}
		}
	}
}

func (w *HTTPExporter) resumeProcessing() {
	select {
	case w.triggers <- struct{}{}:
	default:
		// Exporter processing already triggered
		// This is expected behavior if the exporter is already processing logs
	}
}

func (w *HTTPExporter) Write(logs []byte) (int, error) {
	// Drop oversized lines: Loki would reject them anyway.
	if len(logs) > maxLogLineBytes {
		w.oversizedLog.log(maxLogLineBytes)

		return len(logs), nil
	}

	logsCopy := make([]byte, len(logs))
	copy(logsCopy, logs)

	// Enqueue synchronously so a completed Write is visible to flush barriers.
	// addLogs only holds logLock for in-memory queue bookkeeping, never I/O.
	w.addLogs(logsCopy)

	return len(logs), nil
}

func (w *HTTPExporter) getAllLogs() [][]byte {
	w.logLock.Lock()
	defer w.logLock.Unlock()

	logs := w.logs
	w.logs = nil
	w.bufferedBytes = 0

	return logs
}

func (w *HTTPExporter) purgeLogs() {
	w.logLock.Lock()
	defer w.logLock.Unlock()

	w.logs = nil
	w.bufferedBytes = 0
}

func (w *HTTPExporter) addLogs(logs []byte) {
	w.logLock.Lock()
	defer w.logLock.Unlock()

	// Drop the oldest entries to stay under maxBufferedBytes. Happens when
	// the collector is unreachable or the producer outruns the send loop;
	// keeping the queue bounded matters more than not losing old lines.
	for w.bufferedBytes+len(logs) > maxBufferedBytes && len(w.logs) > 0 {
		w.bufferedBytes -= len(w.logs[0])
		w.logs[0] = nil
		w.logs = w.logs[1:]
	}

	w.bufferedBytes += len(logs)
	w.logs = append(w.logs, logs)

	w.resumeProcessing()
}
