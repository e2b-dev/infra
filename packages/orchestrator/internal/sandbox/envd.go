package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	requestTimeout = 50 * time.Millisecond
	loopDelay      = 5 * time.Millisecond
)

// doRequestWithInfiniteRetries does a request with infinite retries until the context is done.
// The parent context should have a deadline or a timeout.
func doRequestWithInfiniteRetries(ctx context.Context, method, address string, requestBody []byte) (*http.Response, error) {
	for {
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		request, err := http.NewRequestWithContext(reqCtx, method, address, bytes.NewReader(requestBody))

		if err != nil {
			cancel()
			return nil, err
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

func (s *Sandbox) syncOldEnvd(ctx context.Context) error {
	address := fmt.Sprintf("http://%s:%d/sync", s.Slot.HostIP(), consts.OldEnvdServerPort)

	response, err := doRequestWithInfiniteRetries(ctx, "POST", address, nil)
	if err != nil {
		return fmt.Errorf("failed to sync envd: %w", err)
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return err
	}

	err = response.Body.Close()
	if err != nil {
		return err
	}

	return nil
}

type PostInitJSONBody struct {
	EnvVars *map[string]string `json:"envVars"`
}

func (s *Sandbox) initEnvd(ctx context.Context, tracer trace.Tracer, envVars map[string]string) error {
	childCtx, childSpan := tracer.Start(ctx, "envd-init")
	defer childSpan.End()

	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIP(), consts.DefaultEnvdServerPort)

	jsonBody := &PostInitJSONBody{
		EnvVars: &envVars,
	}

	envVarsJSON, err := json.Marshal(jsonBody)
	if err != nil {
		return err
	}

	response, err := doRequestWithInfiniteRetries(childCtx, "POST", address, envVarsJSON)
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
