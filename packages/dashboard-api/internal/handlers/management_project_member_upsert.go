package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/management"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// ManagementUpsertProjectMember reconciles an opaque user UUID as a member of a
// project. Idempotent: a repeated push reports the same success as the first.
func (s *APIStore) ManagementUpsertProjectMember(c *gin.Context, teamID api.TeamID, userID api.UserId) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{
		telemetry.WithTeamID(teamID.String()),
		telemetry.WithUserID(userID.String()),
	}

	// Optional in the contract: an absent body is a caller declining to name
	// who added the member, not a malformed request.
	body, err := ginutils.ParseOptionalBody[api.ManagementMemberUpsertRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project member failed",
			fmt.Errorf("parse member upsert request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	change := management.MemberChange{
		ProjectID: teamID,
		Present:   []uuid.UUID{userID},
		AddedBy:   body.AddedBy,
	}

	if err := s.managementService.SetProjectMembers(ctx, change); err != nil {
		s.sendMembershipError(c, err, "upsert project member failed", attrs...)

		return
	}

	c.Status(http.StatusNoContent)
}
