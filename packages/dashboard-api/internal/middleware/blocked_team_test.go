package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBlockedTeamAllowlistKeepsTeamReadsReachable(t *testing.T) {
	t.Parallel()

	get := blockedTeamAllowlist[http.MethodGet]
	assert.Contains(t, get, "/teams/:teamID/limits")
	assert.Contains(t, get, "/teams/:teamID/status")
	assert.Contains(t, get, "/teams/:teamID/members")
	assert.Contains(t, get, "/teams/resolve")
}
