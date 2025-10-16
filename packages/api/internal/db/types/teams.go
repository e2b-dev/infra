package types

import (
	"github.com/e2b-dev/infra/packages/db/pkg/queries"
)

type Team struct {
	*queries.Team

	Limits *TeamLimits
}

func newTeamLimits(
	teamLimits *queries.TeamLimit,
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
	team *queries.Team,
	teamLimits *queries.TeamLimit,
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
