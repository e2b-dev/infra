package handlers

import (
	"time"

	typesteam "github.com/e2b-dev/infra/packages/auth/pkg/types"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

const (
	// enforce a minimum of 1 minute autoresume for now
	minAutoResumeTimeout          = time.Minute
	defaultProxyAutoResumeTimeout = 5 * time.Minute
)

func getTeamPlanLimit(team *typesteam.Team) time.Duration {
	if team == nil || team.Limits == nil {
		return 0
	}

	return time.Duration(team.Limits.MaxLengthHours) * time.Hour
}

func clampAutoResumeTimeout(requestedTimeout, teamPlanLimit time.Duration) time.Duration {
	timeout := requestedTimeout
	if teamPlanLimit > 0 && timeout > teamPlanLimit {
		timeout = teamPlanLimit
	}
	if timeout < minAutoResumeTimeout {
		timeout = minAutoResumeTimeout
	}

	return timeout
}

func calculateTimeout(requestedTimeout time.Duration, team *typesteam.Team) time.Duration {
	return clampAutoResumeTimeout(requestedTimeout, getTeamPlanLimit(team))
}

func calculateAutoResumeTimeout(autoResume *dbtypes.SandboxAutoResumeConfig, team *typesteam.Team) time.Duration {
	timeout := defaultProxyAutoResumeTimeout
	if autoResume != nil && autoResume.StartingTimeout != nil && *autoResume.StartingTimeout > 0 {
		timeout = time.Duration(*autoResume.StartingTimeout) * time.Second
	}

	return clampAutoResumeTimeout(timeout, getTeamPlanLimit(team))
}
