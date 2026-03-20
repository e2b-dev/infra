package metrics

import (
	"context"
	"errors"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/metrics")

var (
	callsCounter = utils.Must(meter.Int64Counter("orchestrator.nfsproxy.calls.total",
		metric.WithDescription("Total number of calls to the NFS proxy"),
		metric.WithUnit("1")))
	durationRecorder = utils.Must(meter.Int64Histogram("orchestrator.nfsproxy.call.duration",
		metric.WithDescription("Duration of calls to the NFS proxy"),
		metric.WithUnit("ms")))
)

var (
	operationKey = attribute.Key("operation")
	resultKey    = attribute.Key("result")
)

const (
	resultSuccess     = "success"
	resultClientError = "client_error"
	resultOtherError  = "other_error"
)

type finishFunc func(error)

func recordCall(ctx context.Context, operation string) finishFunc {
	start := time.Now()

	return func(err error) {
		result := classifyResult(err)
		durationMs := time.Since(start).Milliseconds()

		attrs := metric.WithAttributes(
			operationKey.String(operation),
			resultKey.String(result),
		)

		callsCounter.Add(ctx, 1, attrs)
		durationRecorder.Record(ctx, durationMs, attrs)
	}
}

func classifyResult(err error) string {
	if err == nil {
		return resultSuccess
	}

	if isClientError(err) {
		return resultClientError
	}

	return resultOtherError
}

func isClientError(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}

	if errors.Is(err, os.ErrExist) {
		return true
	}

	return false
}
