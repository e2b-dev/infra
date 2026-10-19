package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func GetTeamsByUser(ctx context.Context, db *authdb.Client, userID uuid.UUID) ([]*types.TeamWithDefault, error) {
	teams, err := db.GetUserTeamsWithLimits(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("error when getting default team: %w", err)
	}

	teamsWithLimits := make([]*types.TeamWithDefault, 0, len(teams))
	for _, team := range teams {
		// A deleted team keeps its memberships but must not be selectable.
		if team.Team.DeletedAt != nil {
			continue
		}
		teamsWithLimits = append(teamsWithLimits, &types.TeamWithDefault{
			Team:      types.NewTeam(teamRow(team)),
			IsDefault: team.IsDefault,
		})
	}

	return teamsWithLimits, nil
}

// teamRow drops is_default, the one column a single-team lookup lacks.
// TestTeamRowCopiesEveryColumn fails when a column is left behind.
func teamRow(row authqueries.GetUserTeamsWithLimitsRow) authqueries.GetTeamByTeamIDRow {
	return authqueries.GetTeamByTeamIDRow{
		Team:                     row.Team,
		MaxLengthHours:           row.MaxLengthHours,
		ConcurrentSandboxes:      row.ConcurrentSandboxes,
		ConcurrentTemplateBuilds: row.ConcurrentTemplateBuilds,
		MaxVcpu:                  row.MaxVcpu,
		MaxRamMb:                 row.MaxRamMb,
		DiskMb:                   row.DiskMb,
		EventsTtlDays:            row.EventsTtlDays,
		DefaultFreeDiskSizeMb:    row.DefaultFreeDiskSizeMb,
		MaxDiskSizeMb:            row.MaxDiskSizeMb,
		MaxFreeDiskSizeMb:        row.MaxFreeDiskSizeMb,
		ApiTeamRpsList:           row.ApiTeamRpsList,
		ApiTeamRpsDelete:         row.ApiTeamRpsDelete,
		MaxSecrets:               row.MaxSecrets,
	}
}
