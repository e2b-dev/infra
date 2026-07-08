package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) sendProvisioningError(ctx context.Context, c *gin.Context, operation string, err error) {
	attrs := []attribute.KeyValue{
		attribute.String("team.provision.operation", operation),
	}

	var provisionErr *internalteamprovision.ProvisionError
	if errors.As(err, &provisionErr) {
		if provisionErr.StatusCode < http.StatusBadRequest || provisionErr.StatusCode >= 600 {
			telemetry.ReportErrorByCode(ctx, http.StatusInternalServerError, operation+" failed", err, attrs...)
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to "+operation)

			return
		}

		telemetry.ReportErrorByCode(ctx, provisionErr.StatusCode, operation+" failed", err, attrs...)
		s.sendAPIStoreError(c, provisionErr.StatusCode, provisionErr.Error())

		return
	}

	telemetry.ReportErrorByCode(ctx, http.StatusInternalServerError, operation+" failed", err, attrs...)
	s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to "+operation)
}
