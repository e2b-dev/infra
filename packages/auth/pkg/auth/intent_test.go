package auth

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

func newTestTeam(t *testing.T, blocked, banned bool, reason string) *types.Team {
	t.Helper()

	row := &authqueries.Team{
		IsBlocked: blocked,
		IsBanned:  banned,
	}
	if reason != "" {
		r := reason
		row.BlockedReason = &r
	}

	return types.NewTeam(row, &authqueries.TeamLimit{})
}

func TestAuthorizeTeam(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		team        *types.Team
		intent      ActionIntent
		wantErrType any
		wantMsgHas  string
	}{
		// Banned: always denied regardless of intent.
		{"banned + view", newTestTeam(t, false, true, ""), IntentView, &TeamForbiddenError{}, "banned"},
		{"banned + create", newTestTeam(t, false, true, ""), IntentCreate, &TeamForbiddenError{}, "banned"},
		{"banned + delete", newTestTeam(t, false, true, ""), IntentDelete, &TeamForbiddenError{}, "banned"},

		// Not blocked: always allowed.
		{"clean + view", newTestTeam(t, false, false, ""), IntentView, nil, ""},
		{"clean + create", newTestTeam(t, false, false, ""), IntentCreate, nil, ""},
		{"clean + mutate", newTestTeam(t, false, false, ""), IntentMutate, nil, ""},
		{"clean + delete", newTestTeam(t, false, false, ""), IntentDelete, nil, ""},

		// Blocked: View / Delete / Billing allowed.
		{"blocked + view", newTestTeam(t, true, false, "verification required"), IntentView, nil, ""},
		{"blocked + delete", newTestTeam(t, true, false, "verification required"), IntentDelete, nil, ""},

		// Blocked: Create / Mutate denied, reason embedded in message.
		{"blocked + create + reason", newTestTeam(t, true, false, "verification required"), IntentCreate, &TeamBlockedError{}, "verification required"},
		{"blocked + mutate + reason", newTestTeam(t, true, false, "billing limit"), IntentMutate, &TeamBlockedError{}, "billing limit"},
		{"blocked + create no reason", newTestTeam(t, true, false, ""), IntentCreate, &TeamBlockedError{}, "team is blocked"},

		// Unknown intent: fail closed for blocked teams.
		{"blocked + unknown intent", newTestTeam(t, true, false, "missing payment"), ActionIntent("frobnicate"), &TeamBlockedError{}, "team is blocked"},
		// Unknown intent: clean teams still pass (no enforcement applies).
		{"clean + unknown intent", newTestTeam(t, false, false, ""), ActionIntent("frobnicate"), nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := AuthorizeTeam(*tc.team, tc.intent)
			if tc.wantErrType == nil {
				assert.NoError(t, err)

				return
			}
			require.Error(t, err)
			switch tc.wantErrType.(type) {
			case *TeamForbiddenError:
				var typed *TeamForbiddenError
				assert.True(t, errors.As(err, &typed), "expected TeamForbiddenError")
			case *TeamBlockedError:
				var typed *TeamBlockedError
				assert.True(t, errors.As(err, &typed), "expected TeamBlockedError")
			}
			if tc.wantMsgHas != "" {
				assert.Contains(t, err.Error(), tc.wantMsgHas)
			}
		})
	}
}

func TestAuthorizeTeam_NilTeamRow(t *testing.T) {
	t.Parallel()

	// types.Team with a nil embedded row is a programming error; the function
	// returns a generic error rather than panicking.
	bad := &types.Team{}
	err := AuthorizeTeam(*bad, IntentView)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "team is nil")
}

func TestSetAndGetIntent(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	_, ok := GetIntent(c)
	assert.False(t, ok)

	SetIntent(c, IntentCreate)
	got, ok := GetIntent(c)
	assert.True(t, ok)
	assert.Equal(t, IntentCreate, got)
}

func TestAuthorizeTeamCtx(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	t.Run("nil team", func(t *testing.T) {
		t.Parallel()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		SetIntent(c, IntentCreate)
		err := AuthorizeTeamCtx(c, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "team is nil")
	})

	t.Run("blocked + create denied", func(t *testing.T) {
		t.Parallel()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		SetIntent(c, IntentCreate)
		team := newTestTeam(t, true, false, "verification required")
		err := AuthorizeTeamCtx(c, team)
		require.Error(t, err)
		var blocked *TeamBlockedError
		assert.True(t, errors.As(err, &blocked))
	})

	t.Run("blocked + view allowed", func(t *testing.T) {
		t.Parallel()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		SetIntent(c, IntentView)
		team := newTestTeam(t, true, false, "billing limit")
		err := AuthorizeTeamCtx(c, team)
		assert.NoError(t, err)
	})

	t.Run("missing intent fails closed as Mutate", func(t *testing.T) {
		t.Parallel()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		// Intentionally no SetIntent — middleware would normally guarantee
		// it. When missing, AuthorizeTeamCtx treats the action as
		// Mutate (deny).
		team := newTestTeam(t, true, false, "verification required")
		err := AuthorizeTeamCtx(c, team)
		require.Error(t, err)
		var blocked *TeamBlockedError
		assert.True(t, errors.As(err, &blocked))
	})

	t.Run("clean team always allowed", func(t *testing.T) {
		t.Parallel()

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		// No intent set; clean team is allowed regardless.
		team := newTestTeam(t, false, false, "")
		err := AuthorizeTeamCtx(c, team)
		assert.NoError(t, err)
	})
}
