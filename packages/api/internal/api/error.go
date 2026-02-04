package api

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var _ error = (*APIError)(nil)

type APIError struct {
	Err       error
	ClientMsg string
	Code      int
}

func (e *APIError) Error() string {
	return e.Err.Error()
}

func (e *APIError) Report(ctx context.Context, message string, attrs ...attribute.KeyValue) {
	if e.Code >= http.StatusInternalServerError {
		telemetry.ReportCriticalError(ctx, message, e.Err, attrs...)
	} else {
		telemetry.ReportError(ctx, message, e.Err, attrs...)
	}
}
