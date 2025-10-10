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
	extraMaxSandboxes int64,
	extraMaxConcurrentBuilds int64,
	extraMaxVcpu int64,
	extraMaxRamMb int64,
	addonsDiskMb int64,
) *TeamLimits {
	return &TeamLimits{
		SandboxConcurrency: tier.ConcurrentInstances + extraMaxSandboxes,
		BuildConcurrency:   tier.ConcurrentTemplateBuilds + extraMaxConcurrentBuilds,
		MaxLengthHours:     tier.MaxLengthHours,

		MaxVcpu:  tier.MaxVcpu + extraMaxVcpu,
		MaxRamMb: tier.MaxRamMb + extraMaxRamMb,
		DiskMb:   tier.DiskMb + addonsDiskMb,
	}
}

func NewTeam(
	team *queries.Team,
	tier *queries.Tier,
	extraMaxSandboxes int64,
	extraMaxConcurrentBuilds int64,
	extraMaxVcpu int64,
	extraMaxRamMb int64,
	extraDiskMb int64,
) *Team {
	return &Team{
		Team: team,
		Limits: newTeamLimits(
			tier,
			extraMaxSandboxes,
			extraMaxConcurrentBuilds,
			extraMaxVcpu,
			extraMaxRamMb,
			extraDiskMb,
		),
	}
}

type TeamWithDefault struct {
	*Team

	IsDefault bool
}
