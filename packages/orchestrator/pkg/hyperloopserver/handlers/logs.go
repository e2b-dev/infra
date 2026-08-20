//go:build linux

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	logForwardMeter      = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/hyperloopserver/handlers")
	logForwardWriteCount = mustLogForwardCounter(
		"hyperloop_log_forward_write_count",
		"Number of hyperloop log forward HTTP attempts by route and result",
	)
)

func mustLogForwardCounter(name, description string) metric.Int64Counter {
	counter, err := logForwardMeter.Int64Counter(name, metric.WithDescription(description))
	if err != nil {
		return nil
	}

	return counter
}

func (h *APIStore) Logs(c *gin.Context) {
	ctx := c.Request.Context()
	sbx, err := h.sandboxes.GetByHostPort(c.Request.RemoteAddr)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Error when finding source sandbox")
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		h.logger.Error(ctx, "error finding sandbox for source addr", logger.WithSandboxIP(ip), zap.Error(err))

		return
	}

	sbxID := sbx.Runtime.SandboxID

	payload := make(map[string]any)
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Invalid body for logs")
		h.logger.Error(ctx, "error when parsing sandbox logs request", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}

	err = h.validatePayloadSandboxID(payload, sbxID)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandboxID in logs payload")
		// Inflight logs with old sandboxID from snapshotted sandbox
		// Change to error once we have a way how to tell sandbox to flush and stop sending logs when being paused
		h.logger.Warn(ctx, "error when parsing sandbox logs request", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}

	if hasStaleLogTimestamp(payload, sbx.LifecycleStartedAt) {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Log timestamp predates this sandbox's resume")
		h.logger.Warn(ctx, "dropping envd log with a stale pre-resume timestamp", logger.WithSandboxID(sbxID))

		return
	}

	// Overwrite instanceID, envID, and teamID to avoid spoofing
	payload["instanceID"] = sbxID
	payload["envID"] = sbx.Runtime.TemplateID
	payload["teamID"] = sbx.Runtime.TeamID

	logs, err := json.Marshal(payload)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when parsing logs payload")
		h.logger.Error(ctx, "error when parsing logs payload", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}

	// CLICKHOUSE_LOGS_WRITE_ONLY selects exactly one destination. In BYOC the
	// collector owns the Loki and ClickHouse fan-out; managed cloud writes only
	// to the local ClickHouse endpoint. There is no fallback or shadow write.
	if err := h.forwardLogs(c.Request.Context(), h.sandboxLogsAddr, logs, 0); err != nil {
		recordLogForwardWrite(ctx, "primary", "failure", "send_error")
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when forwarding sandbox logs")
		h.logger.Error(ctx, "error when forwarding sandbox logs", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}
	recordLogForwardWrite(ctx, "primary", "success", "")

	c.Status(http.StatusOK)
}

func recordLogForwardWrite(ctx context.Context, route, result, reason string) {
	if logForwardWriteCount == nil {
		return
	}

	logForwardWriteCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("route", route),
		attribute.String("result", result),
		attribute.String("reason", reason),
	))
}

// forwardLogs POSTs the marshaled logs payload to url, bounded by timeout.
func (h *APIStore) forwardLogs(ctx context.Context, url string, payload []byte, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("error creating request to forward sandbox logs: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")
	response, err := h.collectorClient.Do(request)
	if err != nil {
		return fmt.Errorf("error forwarding sandbox logs: %w", err)
	}
	defer response.Body.Close()

	// Always drain so the transport can reuse the connection; the body itself
	// is never surfaced in the returned error (it may echo request content).
	drainErr := drainLogForwardResponse(response.Body)

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		statusErr := fmt.Errorf("error forwarding sandbox logs: unexpected HTTP status %d", response.StatusCode)

		return errors.Join(statusErr, drainErr)
	}

	return drainErr
}

func drainLogForwardResponse(body io.Reader) error {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return fmt.Errorf("error draining log forward response body: %w", err)
	}

	return nil
}

// validatePayloadSandboxID checks if the payload contains correct instanceID to prevent slow requests to contaminating the logs of other sandboxes.
func (h *APIStore) validatePayloadSandboxID(payload map[string]any, sbxID string) error {
	if payload["instanceID"] == nil {
		return errors.New("missing sandboxID in logs payload")
	}

	payloadSandboxID, ok := payload["instanceID"].(string)
	if !ok {
		return fmt.Errorf("instanceID in logs payload is not a string: %v", payload["instanceID"])
	}

	if payloadSandboxID != sbxID {
		return fmt.Errorf("sandboxID in logs payload does not match the sandboxID of the source sandbox (%s != %s)", payloadSandboxID, sbxID)
	}

	return nil
}

// Matches envd's zerolog timestamp format.
const envdTimestampLayout = time.RFC3339Nano

// Allows normal host/guest clock skew.
const clockSkewTolerance = time.Minute

// True if timestamp predates resume.
func hasStaleLogTimestamp(payload map[string]any, lifecycleStart time.Time) bool {
	if lifecycleStart.IsZero() {
		return false
	}

	raw, ok := payload["timestamp"].(string)
	if !ok {
		return false
	}

	ts, err := time.Parse(envdTimestampLayout, raw)
	if err != nil {
		return false
	}

	return ts.Before(lifecycleStart.Add(-clockSkewTolerance))
}
