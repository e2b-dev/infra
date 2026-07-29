package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/management"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// sendNotImplemented answers the management operations that are declared in
// the contract but not served.
func sendNotImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, api.Error{
		Code:    http.StatusNotImplemented,
		Message: "operation is not implemented",
	})
}

// sendMembershipError maps a membership change's failure onto the contract's
// status codes. Shared, so the caller's retry behaviour cannot depend on which
// route it used.
//
// An unknown project is 404 on every verb, deletes included. The caller reads
// that as convergence when deleting and as divergence otherwise, so answering
// uniformly gives it both without this side guessing which applies.
func (s *APIStore) sendMembershipError(c *gin.Context, err error, operation string, attrs ...attribute.KeyValue) {
	ctx := c.Request.Context()

	if errors.Is(err, management.ErrProjectNotFound) {
		telemetry.ReportErrorByCode(ctx, http.StatusNotFound, operation, err, attrs...)
		s.sendAPIStoreError(c, http.StatusNotFound, "Project not found")

		return
	}

	telemetry.ReportCriticalError(ctx, operation, err, attrs...)
	s.sendAPIStoreError(c, http.StatusInternalServerError, "Error applying membership change")
}
