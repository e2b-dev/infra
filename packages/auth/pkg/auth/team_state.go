package auth

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

// CheckTeamBanned returns *TeamForbiddenError if the team is banned.
// Called inside the shared auth store so every service rejects banned teams
// at auth time without per-handler plumbing.
func CheckTeamBanned(team authqueries.Team) error {
	if team.IsBanned {
		return &TeamForbiddenError{Message: "team is banned"}
	}

	return nil
}

// CheckTeamBlocked returns *TeamBlockedError if the team is blocked.
// Called inline at any handler that creates or mutates a billable resource.
// Each service decides for itself which endpoints need it.
//
// Takes a pointer so handlers can pass the result of GetTeamInfo directly
// without a nil-check — admin / access-token paths have no team and return
// nil here (no-op).
func CheckTeamBlocked(team *types.Team) error {
	if team == nil || team.Team == nil || !team.IsBlocked {
		return nil
	}

	msg := "team is blocked"
	if team.BlockedReason != nil && *team.BlockedReason != "" {
		msg = fmt.Sprintf("%s: %s", msg, *team.BlockedReason)
	}

	return &TeamBlockedError{Message: msg}
}
