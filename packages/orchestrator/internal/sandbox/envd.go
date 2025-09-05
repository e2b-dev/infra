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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	requestTimeout = 50 * time.Millisecond
	loopDelay      = 5 * time.Millisecond
)

// doRequestWithInfiniteRetries does a request with infinite retries until the context is done.
// The parent context should have a deadline or a timeout.
func doRequestWithInfiniteRetries(ctx context.Context, method, address string, requestBody []byte, accessToken *string) (*http.Response, error) {
	ctx, span := tracer.Start(ctx, "doRequestWithInfiniteRetries")
	defer span.End()

	for {
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		response, err := doRequest(reqCtx, method, address, requestBody, accessToken)
		cancel()

		if err == nil {
			return response, nil
		}

		select {
		case <-ctx.Done():
			return nil, spanFailed(ctx, "context canceled", fmt.Errorf("%w with cause: %w", ctx.Err(), context.Cause(ctx)))
		case <-time.After(loopDelay):
		}
	}
}

func spanFailed(ctx context.Context, message string, err error) error {
	if err == nil {
		return nil
	}

	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, message)

	return fmt.Errorf("%s: %w", message, err)
}

func doRequest(ctx context.Context, method, address string, requestBody []byte, accessToken *string) (*http.Response, error) {
	var err error
	ctx, span := tracer.Start(ctx, "doRequest")
	defer span.End()

	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(requestBody))
	if err != nil {
		return nil, spanFailed(ctx, "failed to create request", err)
	}

	// make sure request to already authorized envd will not fail
	// this can happen in sandbox resume and in some edge cases when previous request was success, but we continued
	if accessToken != nil {
		request.Header.Set("X-Access-Token", *accessToken)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, spanFailed(ctx, "request failed", err)
	}

	telemetry.SetAttributes(ctx, attribute.Int("response.status_code", response.StatusCode))
	return response, nil
}

type PostInitJSONBody struct {
	EnvVars     *map[string]string `json:"envVars"`
	AccessToken *string            `json:"accessToken,omitempty"`
}

func (s *Sandbox) initEnvd(ctx context.Context, envVars map[string]string, accessToken *string) error {
	ctx, span := tracer.Start(ctx, "envd-init")
	defer span.End()

	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
	jsonBody := &PostInitJSONBody{
		EnvVars:     &envVars,
		AccessToken: accessToken,
	}

	body, err := json.Marshal(jsonBody)
	if err != nil {
		return spanFailed(ctx, "failed to marshal json body", err)
	}

	response, err := doRequestWithInfiniteRetries(ctx, "POST", address, body, accessToken)
	if err != nil {
		return spanFailed(ctx, "failed to init envd", err)
	}

	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		err := fmt.Errorf("unexpected status code: %d", response.StatusCode)
		return spanFailed(ctx, "failed to init envd", err)
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return spanFailed(ctx, "failed to read response body", err)
	}

	return nil
}
