package handlers

import (
	"time"

	typesteam "github.com/e2b-dev/infra/packages/api/internal/db/types"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

const (
	// minAutoResumeTimeout is the anti-thrash floor for auto-resume.
	minAutoResumeTimeout = time.Minute
	// defaultProxyAutoResumeTimeout is the fallback for legacy snapshots
	// that don't yet store a persisted starting timeout.
	defaultProxyAutoResumeTimeout = 5 * time.Minute
)

// getTeamPlanLimit returns the team's maximum allowed sandbox duration.
// A zero return means "no limit provided".
func getTeamPlanLimit(team *typesteam.Team) time.Duration {
	if team == nil || team.Limits == nil {
		return 0
	}

	return time.Duration(team.Limits.MaxLengthHours) * time.Hour
}

// clampAutoResumeTimeout applies the auto-resume floor and team plan cap.
func clampAutoResumeTimeout(requestedTimeout, teamPlanLimit time.Duration) time.Duration {
	timeout := requestedTimeout
	if timeout < minAutoResumeTimeout {
		timeout = minAutoResumeTimeout
	}
	if teamPlanLimit > 0 && timeout > teamPlanLimit {
		timeout = teamPlanLimit
	}

	return timeout
}

// calculateTimeout computes the timeout persisted at sandbox create time.
// It returns (0, false) when no explicit timeout was requested.
func calculateTimeout(requestedTimeout time.Duration, team *typesteam.Team) (time.Duration, bool) {
	if requestedTimeout <= 0 {
		return 0, false
	}

	return clampAutoResumeTimeout(requestedTimeout, getTeamPlanLimit(team)), true
}

// calculateAutoResumeTimeout computes the timeout used during auto-resume.
// It starts from persisted timeout when present, otherwise uses fallback.
func calculateAutoResumeTimeout(autoResume *dbtypes.SandboxAutoResumeConfig, team *typesteam.Team) time.Duration {
	timeout := defaultProxyAutoResumeTimeout
	if autoResume != nil && autoResume.StartingTimeout != nil && *autoResume.StartingTimeout > 0 {
		timeout = *autoResume.StartingTimeout
	}

	return clampAutoResumeTimeout(timeout, getTeamPlanLimit(team))
}
