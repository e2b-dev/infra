package handlers

import (
	"time"

	typesteam "github.com/e2b-dev/infra/packages/auth/pkg/types"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
)

const (
	defaultProxyAutoResumeTimeout = 5 * time.Minute
)

func getTeamPlanLimit(team *typesteam.Team) time.Duration {
	if team == nil || team.Limits == nil {
		return 0
	}

	return time.Duration(team.Limits.MaxLengthHours) * time.Hour
}

func clampAutoResumeTimeout(requestedTimeout, teamPlanLimit, minAutoResumeTimeout time.Duration) time.Duration {
	timeout := requestedTimeout
	if teamPlanLimit > 0 && timeout > teamPlanLimit {
		timeout = teamPlanLimit
	}
	if timeout < minAutoResumeTimeout {
		timeout = minAutoResumeTimeout
	}

	return timeout
}

func calculateTimeoutSeconds(requestedTimeout, minAutoResumeTimeout time.Duration, team *typesteam.Team) uint64 {
	return uint64(clampAutoResumeTimeout(requestedTimeout, getTeamPlanLimit(team), minAutoResumeTimeout).Seconds())
}

func calculateAutoResumeTimeout(autoResume *dbtypes.SandboxAutoResumeConfig, minAutoResumeTimeout time.Duration, team *typesteam.Team) time.Duration {
	timeout := defaultProxyAutoResumeTimeout
	if autoResume != nil && autoResume.Timeout > 0 {
		timeout = time.Duration(autoResume.Timeout) * time.Second
	}

	return clampAutoResumeTimeout(timeout, getTeamPlanLimit(team), minAutoResumeTimeout)
}
