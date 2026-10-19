package types

import (
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

type Team struct {
	*authqueries.Team

	Limits *TeamLimits
}

func (t *Team) TeamID() string {
	return t.Team.ID.String()
}

func newTeamLimits(
	row *authqueries.GetTeamByTeamIDRow,
) *TeamLimits {
	return &TeamLimits{
		SandboxConcurrency: row.ConcurrentSandboxes,
		BuildConcurrency:   row.ConcurrentTemplateBuilds,
		MaxLengthHours:     row.MaxLengthHours,
		MaxVcpu:            row.MaxVcpu,
		MaxRamMb:           row.MaxRamMb,
		DiskMb:             row.DiskMb,
		EventsTTLDays:      row.EventsTtlDays,
		APITeamRPSList:     row.ApiTeamRpsList,
		APITeamRPSDelete:   row.ApiTeamRpsDelete,

		DefaultFreeDiskSizeMb: row.DefaultFreeDiskSizeMb,
		MaxFreeDiskSizeMb:     row.MaxFreeDiskSizeMb,

		MaxSecrets: row.MaxSecrets,
	}
}

// NewTeam takes the row of a single-team lookup. The other lookups return the
// same columns, so their rows convert to this type; a lookup whose column list
// drifts stops compiling at the conversion.
func NewTeam(row authqueries.GetTeamByTeamIDRow) *Team {
	return &Team{
		Team:   &row.Team,
		Limits: newTeamLimits(&row),
	}
}

type TeamWithDefault struct {
	*Team

	IsDefault bool
}
