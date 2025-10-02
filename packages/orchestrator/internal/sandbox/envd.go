package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	loopDelay = 5 * time.Millisecond
)

// doRequestWithInfiniteRetries does a request with infinite retries until the context is done.
// The parent context should have a deadline or a timeout.
func doRequestWithInfiniteRetries(
	ctx context.Context,
	method,
	address string,
	accessToken *string,
	envdInitRequestTimeout time.Duration,
	envVars map[string]string,
	sandboxID,
	envdVersion,
	hyperloopIP string,
) (*http.Response, int64, error) {
	requestCount := int64(0)
	for {
		now := time.Now()

		jsonBody := &PostInitJSONBody{
			EnvVars:     &envVars,
			HyperloopIP: &hyperloopIP,
			AccessToken: accessToken,
			Timestamp:   &now,
		}

		body, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, requestCount, err
		}

		requestCount++
		reqCtx, cancel := context.WithTimeout(ctx, envdInitRequestTimeout)
		request, err := http.NewRequestWithContext(reqCtx, method, address, bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, requestCount, err
		}

		// make sure request to already authorized envd will not fail
		// this can happen in sandbox resume and in some edge cases when previous request was success, but we continued
		if accessToken != nil {
			request.Header.Set("X-Access-Token", *accessToken)
		}

		response, err := httpClient.Do(request)
		cancel()

		if err == nil {
			return response, requestCount, nil
		}

		zap.L().Warn("failed to do request to envd, retrying", logger.WithSandboxID(sandboxID), logger.WithEnvdVersion(envdVersion), zap.Int64("timeout_ms", envdInitRequestTimeout.Milliseconds()), zap.Error(err))

		select {
		case <-ctx.Done():
			return nil, requestCount, fmt.Errorf("%w with cause: %w", ctx.Err(), context.Cause(ctx))
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

func (s *Sandbox) initEnvd(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "envd-init", trace.WithAttributes(telemetry.WithEnvdVersion(s.Config.Envd.Version)))
	defer span.End()

	attributes := []attribute.KeyValue{telemetry.WithEnvdVersion(s.Config.Envd.Version), attribute.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds())}
	attributesFail := append(attributes, attribute.Bool("success", false))
	attributesSuccess := append(attributes, attribute.Bool("success", true))

	hyperloopIP := s.Slot.HyperloopIPString()
	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	response, count, err := doRequestWithInfiniteRetries(
		ctx,
		http.MethodPost,
		address,
		s.Config.Envd.AccessToken,
		s.internalConfig.EnvdInitRequestTimeout,
		s.Config.Envd.Vars,
		s.Runtime.SandboxID,
		s.Config.Envd.Version,
		hyperloopIP,
	)
	if err != nil {
		envdInitCalls.Add(ctx, count, metric.WithAttributes(attributesFail...))
		return fmt.Errorf("failed to init envd: %w", err)
	}

	if count > 1 {
		// Track failed envd init calls
		envdInitCalls.Add(ctx, count-1, metric.WithAttributes(attributesFail...))
	}

	// Track successful envd init
	envdInitCalls.Add(ctx, 1, metric.WithAttributes(attributesSuccess...))

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
