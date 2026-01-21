package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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
	defaultUser *string,
	defaultWorkdir *string,
) (*http.Response, error) {
	for {
		now := time.Now()

		jsonBody := &PostInitJSONBody{
			EnvVars:        &envVars,
			HyperloopIP:    &hyperloopIP,
			AccessToken:    accessToken,
			Timestamp:      &now,
			DefaultUser:    defaultUser,
			DefaultWorkdir: defaultWorkdir,
		}

		body, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, err
		}

		reqCtx, cancel := context.WithTimeout(ctx, envdInitRequestTimeout)
		request, err := http.NewRequestWithContext(reqCtx, method, address, bytes.NewReader(body))
		if err != nil {
			cancel()

			return nil, err
		}

		// make sure request to already authorized envd will not fail
		// this can happen in sandbox resume and in some edge cases when previous request was success, but we continued
		if accessToken != nil {
			request.Header.Set("X-Access-Token", *accessToken)
		}

		response, err := sandboxHttpClient.Do(request)
		cancel()

		if err == nil {
			return response, nil
		}

		logger.L().Debug(ctx, "failed to do request to envd, retrying", logger.WithSandboxID(sandboxID), logger.WithEnvdVersion(envdVersion), zap.Int64("timeout_ms", envdInitRequestTimeout.Milliseconds()), zap.Error(err))

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w with cause: %w", ctx.Err(), context.Cause(ctx))
		case <-time.After(loopDelay):
		}
	}
}

type PostInitJSONBody struct {
	EnvVars        *map[string]string `json:"envVars"`
	AccessToken    *string            `json:"accessToken,omitempty"`
	HyperloopIP    *string            `json:"hyperloopIP,omitempty"`
	Timestamp      *time.Time         `json:"timestamp,omitempty"`
	DefaultUser    *string            `json:"defaultUser,omitempty"`
	DefaultWorkdir *string            `json:"defaultWorkdir,omitempty"`
}

func (s *Sandbox) initEnvd(ctx context.Context) (e error) {
	ctx, span := tracer.Start(ctx, "envd-init", trace.WithAttributes(telemetry.WithEnvdVersion(s.Config.Envd.Version)))
	defer func() {
		if e != nil {
			span.SetStatus(codes.Error, e.Error())
		}

		span.End()
	}()

	hyperloopIP := s.Slot.HyperloopIPString()
	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	response, err := doRequestWithInfiniteRetries(
		ctx,
		http.MethodPost,
		address,
		s.Config.Envd.AccessToken,
		s.internalConfig.EnvdInitRequestTimeout,
		s.Config.Envd.Vars,
		s.Runtime.SandboxID,
		s.Config.Envd.Version,
		hyperloopIP,
		s.Config.Envd.DefaultUser,
		s.Config.Envd.DefaultWorkdir,
	)
	if err != nil {
		return fmt.Errorf("failed to init envd: %w", err)
	}

	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("failed to read envd init response body: %w", err)
	}

	if response.StatusCode != http.StatusNoContent {
		logger.L().Error(ctx, "envd init request failed",
			logger.WithSandboxID(s.Runtime.SandboxID),
			logger.WithEnvdVersion(s.Config.Envd.Version),
			zap.Int("status_code", response.StatusCode),
			zap.String("response_body", utils.Truncate(string(body), 100)),
		)

		return fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	span.SetStatus(codes.Ok, fmt.Sprintf("envd init returned %d", response.StatusCode))

	return nil
}
