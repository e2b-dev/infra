package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	maxRetries     = 3
	retryDelay     = 1 * time.Second // Exponentially backed-off: delay will be doubled with each retry
	eventsEndpoint = "http://events.e2b.dev"
)

type SandboxInternalEvent struct {
	Path    string         `json:"path"`
	Payload map[string]any `json:"payload"`
}

func PushEvent(event *SandboxInternalEvent) error {
	var resp *http.Response

	jsonData, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	for i := range maxRetries {
		resp, err = makeRequest(event.Path, jsonData)
		if err == nil && resp.StatusCode != http.StatusCreated {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("rate limit exceeded")
			}
			return nil
		}

		if resp != nil {
			resp.Body.Close()
		}

		if i < maxRetries-1 {
			backoffDelay := retryDelay * time.Duration(1<<uint(i))
			time.Sleep(backoffDelay)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to push event after %d retries: %w", maxRetries, err)
	}
	return fmt.Errorf("failed to push event after %d retries: status code %d", maxRetries, resp.StatusCode)
}

func makeRequest(path string, payload []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s/%s", eventsEndpoint, path)
	return http.Post(url, "application/json", bytes.NewBuffer(payload))
}
