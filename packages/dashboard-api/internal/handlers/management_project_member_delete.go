package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/management"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// ManagementDeleteProjectMember removes a project member. Removing one that is
// not there succeeds, because the state the request names already holds.
func (s *APIStore) ManagementDeleteProjectMember(c *gin.Context, teamID api.TeamID, userID api.UserId) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{
		telemetry.WithTeamID(teamID.String()),
		telemetry.WithUserID(userID.String()),
	}

	change := management.MemberChange{
		ProjectID: teamID,
		Absent:    []uuid.UUID{userID},
	}

	if err := s.managementService.SetProjectMembers(ctx, change); err != nil {
		s.sendMembershipError(c, err, "delete project member failed", attrs...)

		return
	}

	c.Status(http.StatusNoContent)
}
