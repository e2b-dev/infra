package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A blocked team keeps read access to its own resources and loses mutations.
// The secrets routes follow the same read-parity rule as every other per-team
// resource: metadata reads stay reachable, create/update/delete do not.
func TestBlockedTeamAllowlistKeepsSecretsReadsAndDeniesMutations(t *testing.T) {
	t.Parallel()

	get := blockedTeamAllowlist[http.MethodGet]
	assert.Contains(t, get, "/secrets")
	assert.Contains(t, get, "/secrets/:secretID")

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for route := range blockedTeamAllowlist[method] {
			assert.NotContains(t, []string{"/secrets", "/secrets/:secretID"}, route,
				"secrets mutations must not be allowlisted for blocked teams")
		}
	}
}
