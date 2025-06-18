package exporter

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
)

const ExporterTimeout = 10 * time.Second

type HTTPExporter struct {
	ctx      context.Context
	client   http.Client
	triggers chan struct{}
	logs     [][]byte
	sync.Mutex
	debug bool
}

func NewHTTPLogsExporter(ctx context.Context, debug bool, mmdsChan <-chan *host.MMDSOpts) *HTTPExporter {
	exporter := &HTTPExporter{
		client: http.Client{
			Timeout: ExporterTimeout,
		},
		triggers: make(chan struct{}, 1),
		debug:    debug,
		ctx:      ctx,
	}

	go exporter.start(mmdsChan)

	return exporter
}

func (w *HTTPExporter) sendInstanceLogs(logs []byte, address string) error {
	request, err := http.NewRequestWithContext(w.ctx, http.MethodPost, address, bytes.NewBuffer(logs))
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

func printLog(logs []byte) {
	fmt.Fprintf(os.Stdout, "%v", string(logs))
}

func (w *HTTPExporter) start(mmdsChan <-chan *host.MMDSOpts) {
	mmdsOpts, ok := <-mmdsChan
	if !ok {
		fmt.Fprintf(os.Stderr, "channel closed when starting exporter")
		return
	}

	if mmdsOpts == nil {
		fmt.Fprintf(os.Stderr, "nil mmds opts received on channel when starting exporter")
		return
	}

	for range w.triggers {
		logs := w.getAllLogs()

		if len(logs) == 0 {
			continue
		}

		if w.debug {
			for _, log := range logs {
				fmt.Fprintf(os.Stdout, "%v", string(log))
			}

			continue
		}

		for _, logLine := range logs {
			if mmdsOpts != nil {
				var err error
				logLine, err := mmdsOpts.AddOptsToJSON(logLine)
				if err != nil {
					log.Printf("error adding instance logging options (%+v) to JSON (%+v) with logs : %v\n", mmdsOpts, logLine, err)

					printLog(logLine)

					continue
				}
			}

			err := w.sendInstanceLogs(logLine, mmdsOpts.Address)
			if err != nil {
				log.Printf("error sending instance logs: %+v", err)

				printLog(logLine)

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
	logsCopy := make([]byte, len(logs))
	copy(logsCopy, logs)

	go w.addLogs(logsCopy)

	return len(logs), nil
}

func (w *HTTPExporter) getAllLogs() [][]byte {
	w.Lock()
	defer w.Unlock()

	logs := w.logs
	w.logs = nil

	return logs
}

func (w *HTTPExporter) addLogs(logs []byte) {
	w.Lock()
	defer w.Unlock()

	w.logs = append(w.logs, logs)

	w.resumeProcessing()
}
