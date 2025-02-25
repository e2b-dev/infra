package logger

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type LogsExporter interface {
	Write(logs []byte) (int, error)
	Start()
}

type HTTPExporter struct {
	sync.Mutex
	ctx       context.Context
	client    http.Client
	logQueue  chan []byte
	debugLogs bool
	address   string
}

var logsExporterMU sync.Mutex

func NewHTTPLogsExporter(ctx context.Context, address string, debugLogs bool) LogsExporter {
	logsExporterMU.Lock()
	defer logsExporterMU.Unlock()

	exporter := &HTTPExporter{
		client: http.Client{
			Timeout: 2 * time.Second,
		},
		logQueue:  make(chan []byte, 2048),
		debugLogs: debugLogs,
		ctx:       ctx,
		address:   address,
	}

	if address == "" {
		fmt.Println("no address provided for logs exporter, logs will not be sent")
	}

	if debugLogs {
		fmt.Println("debug logs enabled")
	}

	go exporter.Start()

	return exporter
}

func (w *HTTPExporter) sendInstanceLogs(logs []byte) error {
	if w.address == "" {
		return nil
	}

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

func (w *HTTPExporter) Start() {
	for logLine := range w.logQueue {
		if w.debugLogs {
			fmt.Print(string(logLine))
		}

		err := w.sendInstanceLogs(logLine)
		if err != nil {
			log.Printf("error sending instance logs: %+v\n", err)
		}
	}
}

func (w *HTTPExporter) Write(log []byte) (int, error) {
	logsCopy := make([]byte, len(log))
	copy(logsCopy, log)

	w.logQueue <- logsCopy

	return len(log), nil
}
