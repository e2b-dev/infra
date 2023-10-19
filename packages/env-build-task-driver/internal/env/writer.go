package env

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	API_HOST      = "http://localhost:50001"
	flushInterval = time.Millisecond * 200
)

type BuildLogsWriter struct {
	httpClient   *http.Client
	inputChannel chan string
	Done         chan struct{}
	envID        string
	buildID      string
	apiSecret    string
}

type LogsData struct {
	APISecret string   `json:"apiSecret"`
	Logs      []string `json:"logs"`
}

func (w BuildLogsWriter) Close() error {
	close(w.inputChannel)

	return nil
}

func (w BuildLogsWriter) sendLogsAPICall(logs []string) error {
	data := LogsData{
		Logs:      logs,
		APISecret: w.apiSecret,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		err = fmt.Errorf("error marshaling json: %w", err)

		return err
	}

	response, err := w.httpClient.Post(API_HOST+"/envs/"+w.envID+"/builds/"+w.buildID+"/logs", "application/json", bytes.NewBuffer(jsonData))

	if err != nil {
		err = fmt.Errorf("error posting logs to API: %w", err)

		return err
	}
	defer response.Body.Close()

	if err != nil {
		err = fmt.Errorf("error posting logs to API: %w", err)
		return err
	}
	return nil
}

func (w BuildLogsWriter) sendToAPI() {
	var logs []string

	timer := time.NewTicker(flushInterval)

forLoop:
	for {
		select {
		case log, open := <-w.inputChannel:
			logs = append(logs, log)
			if !open {
				timer.Stop()

				break forLoop
			}
		case <-timer.C:
			if len(logs) > 0 {
				err := w.sendLogsAPICall(logs)
				if err != nil {
					fmt.Println(err)
				}

				logs = nil // Clear the logs slice
			}
		}
	}

	if len(logs) > 0 {
		err := w.sendLogsAPICall(logs)
		if err != nil {
			fmt.Println(err)
		}
	}

	close(w.Done)
}

func (w BuildLogsWriter) Write(p []byte) (n int, err error) {
	w.inputChannel <- string(p)

	return len(p), nil
}

func NewWriter(envID string, buildID string, apiSecret string) BuildLogsWriter {
	writer := BuildLogsWriter{
		inputChannel: make(chan string, 100),
		Done:         make(chan struct{}),
		httpClient:   &http.Client{},
		envID:        envID,
		buildID:      buildID,
		apiSecret:    apiSecret,
	}

	go writer.sendToAPI()

	return writer
}
