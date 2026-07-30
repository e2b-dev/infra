package handlers

import (
	"errors"
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

// errContradictoryEntries reports a user listed twice in one batch with
// opposing presence.
var errContradictoryEntries = errors.New("a user is listed as both present and absent")

// ManagementBatchSyncProjectMembers reconciles many project memberships at
// once, for the group and directory fan-outs where the per-member routes would
// cost a request each. Shares their implementation, so the two cannot disagree
// about the same desired state.
func (s *APIStore) ManagementBatchSyncProjectMembers(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{telemetry.WithTeamID(teamID.String())}

	// maxItems is enforced by the request validator, so an oversized batch
	// never arrives here.
	entries, err := ginutils.ParseBody[api.ManagementMemberBatchRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "batch member sync failed",
			fmt.Errorf("parse member batch request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	present, absent, err := splitBatchEntries(entries)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "batch member sync failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "A user is listed as both present and absent")

		return
	}

	change := management.MemberChange{
		ProjectID: teamID,
		Present:   present,
		Absent:    absent,
	}

	if err := s.managementService.SetProjectMembers(ctx, change); err != nil {
		s.sendMembershipError(c, err, "batch member sync failed", attrs...)

		return
	}

	c.Status(http.StatusNoContent)
}

// splitBatchEntries partitions stated presence into the two sets the change
// takes. A user listed twice with opposing presence is an error: the contract
// promises entries are independent of order, which such a request breaks, and
// picking a winner would hide the caller's bug behind a converged result.
func splitBatchEntries(entries api.ManagementMemberBatchRequest) (present, absent []uuid.UUID, err error) {
	stated := make(map[uuid.UUID]bool, len(entries))

	for _, entry := range entries {
		previous, seen := stated[entry.UserId]
		if seen && previous != entry.Present {
			return nil, nil, fmt.Errorf("%w: %s", errContradictoryEntries, entry.UserId)
		}

		if seen {
			continue
		}

		stated[entry.UserId] = entry.Present

		if entry.Present {
			present = append(present, entry.UserId)
		} else {
			absent = append(absent, entry.UserId)
		}
	}

	return present, absent, nil
}
