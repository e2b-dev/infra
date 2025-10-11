package types

import (
	"github.com/e2b-dev/infra/packages/db/queries"
)

type Team struct {
	*queries.Team

	Limits *TeamLimits
}

func newTeamLimits(
	tier *queries.Tier,
	addons *queries.Addon,
) *TeamLimits {
	return &TeamLimits{
		SandboxConcurrency: tier.ConcurrentInstances + addons.ConcurrentInstances,
		BuildConcurrency:   tier.ConcurrentTemplateBuilds + addons.ConcurrentTemplateBuilds,
		MaxLengthHours:     max(tier.MaxLengthHours, addons.MaxLengthHours),

		MaxVcpu:  tier.MaxVcpu + addons.MaxVcpu,
		MaxRamMb: tier.MaxRamMb + addons.MaxRamMb,
		DiskMb:   max(tier.DiskMb, addons.DiskMb),
	}
}

func NewTeam(
	team *queries.Team,
	tier *queries.Tier,
	addons *queries.Addon,
) *Team {
	return &Team{
		Team:   team,
		Limits: newTeamLimits(tier, addons),
	}
}

type TeamWithDefault struct {
	*Team

	IsDefault bool
}
