package teamprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const billingServerAPIKeyHeader = "X-Billing-Server-API-Key"

const (
	defaultProvisionTimeout          = 30 * time.Second
	defaultProvisionRetryMaxAttempts = 3
	defaultProvisionRetryInitialWait = 100 * time.Millisecond
	defaultProvisionRetryWaitCeiling = 2 * time.Second
	defaultProvisionAttemptTimeout   = defaultProvisionTimeout / defaultProvisionRetryMaxAttempts
	provisionBackoffMultiplier       = 2.0
	// Error responses only need enough body to extract a short API message without buffering large upstream payloads.
	provisionErrorMessageReadLimit = 2 * 1024
	// short cap so a slow auth.users lookup can't eat into the provisioning timeout.
	creatorContextResolveTimeout = 2 * time.Second
)

type HTTPProvisionSink struct {
	baseURL    string
	apiToken   string
	client     *retryablehttp.Client
	timeout    time.Duration
	supabaseDB *supabasedb.Client
}

var _ TeamProvisionSink = (*HTTPProvisionSink)(nil)

type errorResponse struct {
	Message string `json:"message"`
}

func NewHTTPProvisionSink(baseURL, apiToken string, supabaseDB *supabasedb.Client) *HTTPProvisionSink {
	return &HTTPProvisionSink{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiToken:   apiToken,
		client:     newRetryableProvisionClient(defaultProvisionAttemptTimeout),
		timeout:    defaultProvisionTimeout,
		supabaseDB: supabaseDB,
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

	if s.supabaseDB != nil && req.CreatorContext == nil {
		resolveCtx, cancel := context.WithTimeout(ctx, creatorContextResolveTimeout)
		creatorContext, resolveErr := resolveCreatorContext(resolveCtx, s.supabaseDB, req.CreatorUserID)
		cancel()
		if resolveErr != nil {
			// creator context is best-effort; keep going without it
			logger.L().Warn(ctx, "failed to resolve creator context for team provisioning",
				append(provisionLogFields(req, provisionSinkHTTP), zap.Error(resolveErr))...,
			)
		} else {
			req.CreatorContext = creatorContext
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "marshal billing provisioning request", err, baseAttrs...)

		return fmt.Errorf("marshal billing provisioning request: %w", err)
	}

	retryCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	startedAt := time.Now()
	resp, err := s.provisionTeamOnce(retryCtx, body)
	if resp != nil {
		defer resp.Body.Close()
	}

	duration := time.Since(startedAt)
	if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
		successAttrs := provisionTelemetryAttrs(req, provisionSinkHTTP,
			attribute.String("team.provision.result", "success"),
			attribute.Int64("http.response.status_code", int64(http.StatusOK)),
			attribute.Int64("team.provision.duration_ms", duration.Milliseconds()),
		)
		telemetry.ReportEvent(ctx, "team_provision.completed", successAttrs...)

		fields := append(provisionLogFields(req, provisionSinkHTTP),
			zap.String("team.provision.result", "success"),
			zap.Int("http.response.status_code", http.StatusOK),
			zap.Duration("team.provision.duration", duration),
		)
		logger.L().Info(ctx, "team provisioning completed", fields...)

		return nil
	}

	provisionErr := buildProvisionError(resp, err)
	failureAttrs := provisionTelemetryAttrs(req, provisionSinkHTTP,
		attribute.String("team.provision.result", "failed"),
		attribute.Int64("team.provision.duration_ms", duration.Milliseconds()),
		attribute.Int64("http.response.status_code", int64(provisionErr.StatusCode)),
	)
	telemetry.ReportErrorByCode(ctx, provisionErr.StatusCode, "team provisioning failed", provisionErr, failureAttrs...)

	return provisionErr
}

func (s *HTTPProvisionSink) provisionTeamOnce(ctx context.Context, body []byte) (*http.Response, error) {
	httpReq, err := retryablehttp.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.baseURL+"/internal/teams/provision",
		body,
	)
	if err != nil {
		return nil, fmt.Errorf("create billing provisioning request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(billingServerAPIKeyHeader, s.apiToken)

	return s.client.Do(httpReq)
}

func buildProvisionError(resp *http.Response, err error) *ProvisionError {
	if resp != nil {
		message, readErr := readProvisionErrorMessage(resp)
		if readErr != nil {
			return &ProvisionError{
				StatusCode: http.StatusBadGateway,
				Message:    "billing provisioning response was unreadable",
				Err:        fmt.Errorf("read billing provisioning error response: %w", readErr),
			}
		}

		return &ProvisionError{
			StatusCode: resp.StatusCode,
			Message:    message,
			Err:        err,
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &ProvisionError{
			StatusCode: http.StatusGatewayTimeout,
			Message:    "billing provisioning request timed out",
			Err:        err,
		}
	}
	if errors.Is(err, context.Canceled) {
		return &ProvisionError{
			StatusCode: http.StatusServiceUnavailable,
			Message:    "billing provisioning request was canceled",
			Err:        err,
		}
	}

	return &ProvisionError{
		StatusCode: http.StatusServiceUnavailable,
		Message:    "billing provisioning request failed",
		Err:        err,
	}
}

func newRetryableProvisionClient(timeout time.Duration) *retryablehttp.Client {
	client := retryablehttp.NewClient()
	client.Logger = nil
	client.RetryMax = defaultProvisionRetryMaxAttempts - 1
	client.RetryWaitMin = defaultProvisionRetryInitialWait
	client.RetryWaitMax = defaultProvisionRetryWaitCeiling
	client.ErrorHandler = retryablehttp.PassthroughErrorHandler
	client.Backoff = func(minWait, maxWait time.Duration, attemptNum int, resp *http.Response) time.Duration {
		if resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) {
			return retryablehttp.DefaultBackoff(minWait, maxWait, attemptNum, resp)
		}

		backoff := minWait
		for range attemptNum {
			backoff = time.Duration(float64(backoff) * provisionBackoffMultiplier)
			if backoff > maxWait {
				backoff = maxWait

				break
			}
		}

		if backoff > 0 {
			return time.Duration(rand.Int63n(int64(backoff)))
		}

		return backoff
	}
	client.HTTPClient.Timeout = timeout
	client.HTTPClient.Transport = otelhttp.NewTransport(client.HTTPClient.Transport)

	return client
}

func readProvisionErrorMessage(resp *http.Response) (string, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, provisionErrorMessageReadLimit))
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
