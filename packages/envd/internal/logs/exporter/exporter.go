package exporter

import (
	"bytes"
	"context"
	"log"
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
)

type HTTPExporter struct {
	client        http.Client
	logs          [][]byte
	bufferedBytes int
	mmdsOpts      atomic.Pointer[host.MMDSOpts]

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
		triggers: make(chan struct{}, 1),
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

	return nil
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
	// Cap stderr noise: log each error kind at most once per logFloor so a
	// fast-failing collector (e.g. TCP RST) can't flood journald. The
	// suppressed count is included on the next emitted line.
	const logFloor = time.Minute
	var lastLoggedJSONErr, lastLoggedSendErr time.Time
	var suppressedJSONErrs, suppressedSendErrs int

	for range w.triggers {
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
				if time.Since(lastLoggedJSONErr) > logFloor {
					log.Printf("error adding instance logging options to JSON (%d suppressed since last log): %v", suppressedJSONErrs, err)
					lastLoggedJSONErr = time.Now()
					suppressedJSONErrs = 0
				} else {
					suppressedJSONErrs++
				}

				continue
			}

			if err := w.sendInstanceLogs(ctx, logLineWithOpts, opts.LogsCollectorAddress); err != nil {
				if time.Since(lastLoggedSendErr) > logFloor {
					log.Printf("error sending instance logs (%d suppressed since last log): %+v", suppressedSendErrs, err)
					lastLoggedSendErr = time.Now()
					suppressedSendErrs = 0
				} else {
					suppressedSendErrs++
				}

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
		return len(logs), nil
	}

	logsCopy := make([]byte, len(logs))
	copy(logsCopy, logs)

	go w.addLogs(logsCopy)

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
