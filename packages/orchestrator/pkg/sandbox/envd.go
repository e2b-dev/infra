//go:build linux

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
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/envd"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	loopDelay = 5 * time.Millisecond
)

type envdCgroupOp string

const (
	envdCgroupOpFreeze   envdCgroupOp = "freeze"
	envdCgroupOpUnfreeze envdCgroupOp = "unfreeze"
)

// doRequestWithInfiniteRetries does a request with infinite retries until the context is done.
// The parent context should have a deadline or a timeout.
func (s *Sandbox) doRequestWithInfiniteRetries(
	ctx context.Context,
	method,
	address string,
) (*http.Response, int64, error) {
	requestCount := int64(0)

	jsonBody := &envd.PostInitJSONBody{
		LifecycleID:    s.LifecycleID,
		EnvVars:        s.Config.Envd.Vars,
		HyperloopIP:    s.config.NetworkConfig.OrchestratorInSandboxIPAddress,
		AccessToken:    utils.DerefOrDefault(s.Config.Envd.AccessToken, ""),
		DefaultUser:    utils.DerefOrDefault(s.Config.Envd.DefaultUser, ""),
		DefaultWorkdir: utils.DerefOrDefault(s.Config.Envd.DefaultWorkdir, ""),
		VolumeMounts:   s.convertMounts(s.Config.VolumeMounts),
		CaBundle:       s.CABundle,
	}

	for {
		jsonBody.Timestamp = time.Now()

		body, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, requestCount, err
		}

		requestCount++
		reqCtx, cancel := context.WithTimeout(ctx, s.internalConfig.EnvdInitRequestTimeout)
		request, err := http.NewRequestWithContext(reqCtx, method, address, bytes.NewReader(body))
		if err != nil {
			cancel()

			return nil, requestCount, err
		}

		// make sure request to already authorized envd will not fail
		// this can happen in sandbox resume and in some edge cases when previous request was success, but we continued
		if s.Config.Envd.AccessToken != nil {
			request.Header.Set("X-Access-Token", *s.Config.Envd.AccessToken)
		}

		response, err := sandboxHttpClient.Do(request)
		cancel()

		if err == nil {
			return response, requestCount, nil
		}

		select {
		case <-ctx.Done():
			return nil, requestCount, fmt.Errorf("%w with cause: %w", ctx.Err(), context.Cause(ctx))
		case <-time.After(loopDelay):
		}
	}
}

// callEnvdFreeze calls envd's native POST /freeze endpoint to freeze
// user/pty cgroups directly (no Process.Start, no shell). Used pre-pause
// with a tight, freeze-only timeout.
func (s *Sandbox) callEnvdFreeze(ctx context.Context, timeout time.Duration) error {
	return s.callEnvdCgroupOp(ctx, timeout, envdCgroupOpFreeze)
}

// callEnvdUnfreeze calls envd's native POST /unfreeze endpoint. Reserved for
// the pause-failure rollback path; the resume thaw runs via /init's deferred
// unfreeze and does not use this.
func (s *Sandbox) callEnvdUnfreeze(ctx context.Context, timeout time.Duration) error {
	return s.callEnvdCgroupOp(ctx, timeout, envdCgroupOpUnfreeze)
}

func (s *Sandbox) callEnvdCgroupOp(ctx context.Context, timeout time.Duration, op envdCgroupOp) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := fmt.Sprintf("http://%s:%d/%s", s.Slot.HostIPString(), consts.DefaultEnvdServerPort, op)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, nil)
	if err != nil {
		return fmt.Errorf("build %s request: %w", op, err)
	}
	if s.Config.Envd.AccessToken != nil {
		req.Header.Set("X-Access-Token", *s.Config.Envd.AccessToken)
	}

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request: %w", op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("%s returned %d: %s", op, resp.StatusCode, utils.Truncate(string(body), 100))
	}

	return nil
}

func (s *Sandbox) convertMounts(mounts []VolumeMountConfig) []envd.VolumeMount {
	results := make([]envd.VolumeMount, 0, len(mounts))

	for _, mount := range mounts {
		results = append(results, envd.VolumeMount{
			NfsTarget: fmt.Sprintf("%s:/%s", s.config.NetworkConfig.OrchestratorInSandboxIPAddress, mount.Name),
			Path:      mount.Path,
		})
	}

	return results
}

func (s *Sandbox) initEnvd(ctx context.Context) (e error) {
	ctx, span := tracer.Start(ctx, "envd-init", trace.WithAttributes(telemetry.WithEnvdVersion(s.Config.Envd.Version)))
	defer func() {
		if e != nil {
			span.SetStatus(codes.Error, e.Error())
		}

		span.End()
	}()

	attributes := []attribute.KeyValue{telemetry.WithEnvdVersion(s.Config.Envd.Version), attribute.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds())}
	attributesFail := append(attributes, attribute.Bool("success", false))
	attributesSuccess := append(attributes, attribute.Bool("success", true))

	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	response, count, err := s.doRequestWithInfiniteRetries(ctx, http.MethodPost, address)
	if err != nil {
		logger.L().Error(ctx, "failed to init envd after retries",
			logger.WithSandboxID(s.Runtime.SandboxID),
			logger.WithEnvdVersion(s.Config.Envd.Version),
			zap.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds()),
			zap.Int64("attempts", count),
			zap.Error(err),
		)

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

	logger.L().Debug(ctx, "succeeded to init envd",
		logger.WithSandboxID(s.Runtime.SandboxID),
		logger.WithEnvdVersion(s.Config.Envd.Version),
		zap.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds()),
		zap.Int64("attempts", count),
	)

	span.SetStatus(codes.Ok, fmt.Sprintf("envd init returned %d", response.StatusCode))

	return nil
}
