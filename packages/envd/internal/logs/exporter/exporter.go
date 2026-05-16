package exporter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
)

const (
	ExporterTimeout = 10 * time.Second

	// maxBufferedLogs caps the in-memory log queue so that if the orchestrator
	// log collector is unreachable, w.logs cannot grow unbounded. Oldest logs
	// are dropped under back-pressure.
	maxBufferedLogs = 10000

	// Exporter send-failure backoff: each failure waits sendInitialBackoff,
	// doubling up to sendMaxBackoff, before any further send is attempted.
	// During the cooldown window logs are dropped (we already report the
	// outage to journald and the orchestrator was supposed to receive them
	// anyway). Resets to the initial value on the next successful send.
	sendInitialBackoff = 1 * time.Second
	sendMaxBackoff     = 5 * time.Minute
)

type HTTPExporter struct {
	client   http.Client
	logs     [][]byte
	isNotFC  bool
	verbose  bool
	mmdsOpts *host.MMDSOpts

	// Concurrency coordination
	triggers  chan struct{}
	logLock   sync.RWMutex
	mmdsLock  sync.RWMutex
	startOnce sync.Once
}

func NewHTTPLogsExporter(ctx context.Context, isNotFC bool, verbose bool, mmdsChan <-chan *host.MMDSOpts) *HTTPExporter {
	exporter := &HTTPExporter{
		client: http.Client{
			Timeout: ExporterTimeout,
		},
		triggers:  make(chan struct{}, 1),
		isNotFC:   isNotFC,
		verbose:   verbose,
		startOnce: sync.Once{},
		mmdsOpts: &host.MMDSOpts{
			SandboxID:            "unknown",
			TemplateID:           "unknown",
			LogsCollectorAddress: "",
		},
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

func (w *HTTPExporter) printLog(logs []byte) {
	// Only emit on -verbose. Inside FC stdout flows into journald, which is
	// what this PR is trying to keep clean during send outages.
	if !w.verbose {
		return
	}
	fmt.Fprintf(os.Stdout, "%v", string(logs))
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

			w.mmdsLock.Lock()
			w.mmdsOpts.Update(mmdsOpts.SandboxID, mmdsOpts.TemplateID, mmdsOpts.LogsCollectorAddress)
			w.mmdsLock.Unlock()

			w.startOnce.Do(func() {
				go w.start(ctx)
			})
		}
	}
}

func (w *HTTPExporter) start(ctx context.Context) {
	// Suppress repeated failures so a wedged collector doesn't 1:1-amplify
	// envd logs into journald.
	var loggedJSONErr, loggedSendErr bool

	// Exponential backoff between send attempts after a failure, so a
	// persistently broken collector doesn't keep wasting ExporterTimeout
	// (10s) per log line.
	var cooldownUntil time.Time
	cooldown := sendInitialBackoff

	for range w.triggers {
		logs := w.getAllLogs()

		if len(logs) == 0 {
			continue
		}

		if w.isNotFC {
			if w.verbose {
				for _, log := range logs {
					fmt.Fprintf(os.Stdout, "%v", string(log))
				}
			}

			continue
		}

		for _, logLine := range logs {
			w.mmdsLock.RLock()
			logLineWithOpts, err := w.mmdsOpts.AddOptsToJSON(logLine)
			w.mmdsLock.RUnlock()
			if err != nil {
				if !loggedJSONErr {
					fmt.Fprintf(os.Stderr, "<4>error adding instance logging options (%+v) to JSON (%+v) with logs (suppressing further failures): %v\n", w.mmdsOpts, logLine, err)
					loggedJSONErr = true
				}

				w.printLog(logLine)

				continue
			}

			if time.Now().Before(cooldownUntil) {
				// Skip send during cooldown; the log is dropped.
				continue
			}

			err = w.sendInstanceLogs(ctx, logLineWithOpts, w.mmdsOpts.LogsCollectorAddress)
			if err != nil {
				if !loggedSendErr {
					fmt.Fprintf(os.Stderr, "<4>error sending instance logs (suppressing further failures): %v\n", err)
					loggedSendErr = true
				}

				cooldownUntil = time.Now().Add(cooldown)
				cooldown *= 2
				if cooldown > sendMaxBackoff {
					cooldown = sendMaxBackoff
				}

				w.printLog(logLine)

				continue
			}

			cooldown = sendInitialBackoff
			cooldownUntil = time.Time{}
			loggedSendErr = false
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

	return logs
}

func (w *HTTPExporter) addLogs(logs []byte) {
	w.logLock.Lock()
	defer w.logLock.Unlock()

	// Drop oldest under back-pressure so the queue can't grow unbounded if
	// the collector is unreachable.
	if len(w.logs) >= maxBufferedLogs {
		w.logs = w.logs[len(w.logs)-maxBufferedLogs+1:]
	}
	w.logs = append(w.logs, logs)

	w.resumeProcessing()
}
