package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	requestTimeout = 50 * time.Millisecond
	loopDelay      = 5 * time.Millisecond
)

// doRequestWithInfiniteRetries does a request with infinite retries until the context is done.
// The parent context should have a deadline or a timeout.
func doRequestWithInfiniteRetries(ctx context.Context, method, address string, requestBody []byte, accessToken *string) (*http.Response, error) {
	for {
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		request, err := http.NewRequestWithContext(reqCtx, method, address, bytes.NewReader(requestBody))
		if err != nil {
			cancel()
			return nil, err
		}

		// make sure request to already authorized envd will not fail
		// this can happen in sandbox resume and in some edge cases when previous request was success, but we continued
		if accessToken != nil {
			request.Header.Set("X-Access-Token", *accessToken)
		}

		response, err := httpClient.Do(request)
		cancel()

		if err == nil {
			return response, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w with cause: %w", ctx.Err(), context.Cause(ctx))
		case <-time.After(loopDelay):
		}
	}
}

type PostInitJSONBody struct {
	EnvVars     *map[string]string `json:"envVars"`
	AccessToken *string            `json:"accessToken,omitempty"`
	HyperloopIP *string            `json:"hyperloopIP,omitempty"`
	Timestamp   *time.Time         `json:"timestamp,omitempty"`
}

func (s *Sandbox) initEnvd(ctx context.Context, envVars map[string]string, accessToken *string) error {
	childCtx, childSpan := tracer.Start(ctx, "envd-init")
	defer childSpan.End()

	hyperloopIP := s.Slot.HyperloopIPString()
	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	now := time.Now()
	jsonBody := &PostInitJSONBody{
		EnvVars:     &envVars,
		HyperloopIP: &hyperloopIP,
		AccessToken: accessToken,
		Timestamp:   &now,
	}

	body, err := json.Marshal(jsonBody)
	if err != nil {
		return err
	}

	response, err := doRequestWithInfiniteRetries(childCtx, "POST", address, body, accessToken)
	if err != nil {
		return fmt.Errorf("failed to init envd: %w", err)
	}

	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return err
	}

	return nil
}
