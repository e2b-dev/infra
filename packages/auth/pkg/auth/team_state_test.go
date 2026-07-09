package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func TestCheckTeamBanned(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		team    authqueries.Team
		wantErr bool
	}{
		{
			name:    "not banned",
			team:    authqueries.Team{IsBanned: false},
			wantErr: false,
		},
		{
			name:    "banned",
			team:    authqueries.Team{IsBanned: true},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckTeamBanned(tc.team)
			if !tc.wantErr {
				assert.NoError(t, err)

				return
			}

			var forbidden *TeamForbiddenError
			assert.ErrorAs(t, err, &forbidden)
		})
	}
}

func TestCheckTeamBlocked(t *testing.T) {
	t.Parallel()

	reason := "payment failed"
	empty := ""

	cases := []struct {
		name       string
		team       *types.Team
		wantErr    bool
		wantMsgHas string
	}{
		{
			name:    "nil team pointer",
			team:    nil,
			wantErr: false,
		},
		{
			name:    "nil inner team row",
			team:    &types.Team{},
			wantErr: false,
		},
		{
			name:    "not blocked",
			team:    types.NewTeam(&authqueries.Team{IsBlocked: false}, &authqueries.TeamLimit{}),
			wantErr: false,
		},
		{
			name:       "blocked without reason",
			team:       types.NewTeam(&authqueries.Team{IsBlocked: true}, &authqueries.TeamLimit{}),
			wantErr:    true,
			wantMsgHas: "team is blocked",
		},
		{
			name:       "blocked with reason",
			team:       types.NewTeam(&authqueries.Team{IsBlocked: true, BlockedReason: &reason}, &authqueries.TeamLimit{}),
			wantErr:    true,
			wantMsgHas: reason,
		},
		{
			name:       "blocked with empty reason pointer",
			team:       types.NewTeam(&authqueries.Team{IsBlocked: true, BlockedReason: &empty}, &authqueries.TeamLimit{}),
			wantErr:    true,
			wantMsgHas: "team is blocked",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckTeamBlocked(tc.team)
			if !tc.wantErr {
				assert.NoError(t, err)

				return
			}

			var blocked *TeamBlockedError
			require.ErrorAs(t, err, &blocked)
			assert.Contains(t, err.Error(), tc.wantMsgHas)
		})
	}
}
