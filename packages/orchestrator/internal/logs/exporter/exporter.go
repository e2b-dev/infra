package exporter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

type HTTPExporter struct {
	sync.Mutex
	ctx      context.Context
	client   http.Client
	logQueue chan []byte
	debug    bool
	address  string
}

func NewHTTPLogsExporter(ctx context.Context, debug bool, address string) *HTTPExporter {
	exporter := &HTTPExporter{
		client: http.Client{
			Timeout: 2 * time.Second,
		},
		logQueue: make(chan []byte, 128),
		debug:    debug,
		ctx:      ctx,
		address:  address,
	}

	go exporter.start()

	return exporter
}

func (w *HTTPExporter) sendInstanceLogs(logs []byte) error {
	request, err := http.NewRequestWithContext(w.ctx, http.MethodPost, w.address, bytes.NewBuffer(logs))
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

func (w *HTTPExporter) start() {
	for log := range w.logQueue {
		if w.debug {
			fmt.Fprintf(os.Stdout, "%v\n", string(log))
		} else {
			err := w.sendInstanceLogs(log)
			if err != nil {
				fmt.Fprintf(os.Stderr, fmt.Sprintf("error sending instance logs: %+v\n", err))
			}
		}
	}
}

func (w *HTTPExporter) Write(log []byte) (int, error) {
	logsCopy := make([]byte, len(log))
	copy(logsCopy, log)

	w.logQueue <- logsCopy

	return len(log), nil
}
