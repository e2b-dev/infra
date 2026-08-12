package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/management"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// ManagementUpsertProjectLimits records a project's effective limits.
//
// Idempotent, because the caller retries. The revision is what makes that hold
// for a delayed retry too: a delivery at or below the one this side has recorded
// arrived after a newer one and is dropped, and the caller is told 204 either
// way -- what it asked for is stored, and a newer answer is not this call's to
// undo.
func (s *APIStore) ManagementUpsertProjectLimits(c *gin.Context, projectID api.ProjectID) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{telemetry.WithTeamID(projectID.String())}

	body, err := ginutils.ParseBody[api.ManagementProjectLimits](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project limits failed",
			fmt.Errorf("parse project limits request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	if err := s.managementService.ApplyProjectLimits(ctx, management.ProjectLimitsProjection{
		ProjectID:                projectID,
		Revision:                 body.Revision,
		MaxLengthHours:           int64(body.MaxSandboxLengthHours),
		ConcurrentSandboxes:      int64(body.ConcurrentSandboxes),
		ConcurrentTemplateBuilds: int64(body.ConcurrentTemplateBuilds),
		MaxVCPU:                  int64(body.MaxVcpu),
		MaxRAMMB:                 body.MaxRamMb,
		DiskMB:                   body.DiskMb,
		EventsTTLDays:            int64(body.EventsTtlDays),
		DefaultFreeDiskSizeMB:    body.DefaultFreeDiskSizeMb,
		MaxDiskSizeMB:            body.MaxDiskSizeMb,
	}); err != nil {
		s.sendProjectLimitsError(c, err, attrs...)

		return
	}

	c.Status(http.StatusNoContent)
}
