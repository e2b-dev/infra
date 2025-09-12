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
		response, err := doRequest(ctx, method, address, requestBody, accessToken)
		if response != nil {
			return response, nil
		}

		if err != nil {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w with cause: %w", ctx.Err(), context.Cause(ctx))
		case <-time.After(loopDelay):
		}
	}
}

func doRequest(ctx context.Context, method, address string, requestBody []byte, accessToken *string) (*http.Response, error) {
	ctx, span := tracer.Start(ctx, "env-init-request")
	defer span.End()

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// make sure request to already authorized envd will not fail
	// this can happen in sandbox resume and in some edge cases when previous request was success, but we continued
	if accessToken != nil {
		request.Header.Set("X-Access-Token", *accessToken)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		span.AddEvent("request failed")
		return nil, nil
	}

	return response, nil
}

type PostInitJSONBody struct {
	EnvVars     *map[string]string `json:"envVars"`
	AccessToken *string            `json:"accessToken,omitempty"`
}

func (s *Sandbox) initEnvd(ctx context.Context, envVars map[string]string, accessToken *string) error {
	childCtx, childSpan := tracer.Start(ctx, "envd-init")
	defer childSpan.End()

	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	jsonBody := &PostInitJSONBody{
		EnvVars:     &envVars,
		AccessToken: accessToken,
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
