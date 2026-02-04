package types

import (
	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

type Team struct {
	*authqueries.Team

	Limits *TeamLimits
}

func newTeamLimits(
	teamLimits *authqueries.TeamLimit,
) *TeamLimits {
	return &TeamLimits{
		SandboxConcurrency: int64(teamLimits.ConcurrentSandboxes),
		BuildConcurrency:   int64(teamLimits.ConcurrentTemplateBuilds),
		MaxLengthHours:     teamLimits.MaxLengthHours,
		MaxVcpu:            int64(teamLimits.MaxVcpu),
		MaxRamMb:           int64(teamLimits.MaxRamMb),
		DiskMb:             int64(teamLimits.DiskMb),
	}
}

func NewTeam(
	team *authqueries.Team,
	teamLimits *authqueries.TeamLimit,
) *Team {
	return &Team{
		Team:   team,
		Limits: newTeamLimits(teamLimits),
	}
}

type TeamWithDefault struct {
	*Team

	IsDefault bool
}
