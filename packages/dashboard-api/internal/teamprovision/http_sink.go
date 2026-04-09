package teamprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const billingServerAPIKeyHeader = "X-Billing-Server-API-Key"

type HTTPProvisionSink struct {
	baseURL  string
	apiToken string
	client   *http.Client
}

var _ TeamProvisionSink = (*HTTPProvisionSink)(nil)

type errorResponse struct {
	Message string `json:"message"`
}

func NewHTTPProvisionSink(baseURL, apiToken string, timeout time.Duration) *HTTPProvisionSink {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	return &HTTPProvisionSink{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiToken: apiToken,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (s *HTTPProvisionSink) ProvisionTeam(ctx context.Context, req sharedteamprovision.TeamBillingProvisionRequestedV1) error {
	baseAttrs := provisionTelemetryAttrs(req, provisionSinkHTTP)
	telemetry.SetAttributes(ctx, baseAttrs...)
	telemetry.ReportEvent(ctx, "team_provision.started", baseAttrs...)

	if s.baseURL == "" || s.apiToken == "" {
		err := &ProvisionError{
			StatusCode: http.StatusServiceUnavailable,
			Message:    "billing provisioning sink is not configured",
		}
		failureAttrs := provisionTelemetryAttrs(req, provisionSinkHTTP,
			attribute.String("team.provision.result", "failed"),
			attribute.Int64("http.response.status_code", int64(err.StatusCode)),
		)
		telemetry.ReportErrorByCode(ctx, err.StatusCode, "team provisioning failed", err, failureAttrs...)

		return err
	}

	body, err := json.Marshal(req)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "marshal billing provisioning request", err, baseAttrs...)

		return fmt.Errorf("marshal billing provisioning request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.baseURL+"/internal/teams/provision",
		bytes.NewReader(body),
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "create billing provisioning request", err, baseAttrs...)

		return fmt.Errorf("create billing provisioning request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(billingServerAPIKeyHeader, s.apiToken)

	startedAt := time.Now()
	resp, err := s.client.Do(httpReq)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "call billing provisioning endpoint", err, baseAttrs...)

		return fmt.Errorf("call billing provisioning endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		duration := time.Since(startedAt)
		successAttrs := provisionTelemetryAttrs(req, provisionSinkHTTP,
			attribute.String("team.provision.result", "success"),
			attribute.Int64("http.response.status_code", int64(resp.StatusCode)),
			attribute.Int64("team.provision.duration_ms", duration.Milliseconds()),
		)
		telemetry.ReportEvent(ctx, "team_provision.completed", successAttrs...)

		fields := append(provisionLogFields(req, provisionSinkHTTP),
			zap.String("team.provision.result", "success"),
			zap.Int("http.response.status_code", resp.StatusCode),
			zap.Duration("team.provision.duration", duration),
		)
		logger.L().Info(ctx, "team provisioning completed", fields...)

		return nil
	}

	message, err := readProvisionErrorMessage(resp)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "read billing provisioning error response", err, baseAttrs...)

		return fmt.Errorf("read billing provisioning error response: %w", err)
	}

	duration := time.Since(startedAt)
	provisionErr := &ProvisionError{
		StatusCode: resp.StatusCode,
		Message:    message,
	}
	failureAttrs := provisionTelemetryAttrs(req, provisionSinkHTTP,
		attribute.String("team.provision.result", "failed"),
		attribute.Int64("http.response.status_code", int64(resp.StatusCode)),
		attribute.Int64("team.provision.duration_ms", duration.Milliseconds()),
	)
	telemetry.ReportErrorByCode(ctx, resp.StatusCode, "team provisioning failed", provisionErr, failureAttrs...)

	return provisionErr
}

func readProvisionErrorMessage(resp *http.Response) (string, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return "", err
	}

	var apiErr errorResponse
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Message != "" {
		return apiErr.Message, nil
	}

	message := strings.TrimSpace(string(body))
	if message != "" {
		return message, nil
	}

	return http.StatusText(resp.StatusCode), nil
}
