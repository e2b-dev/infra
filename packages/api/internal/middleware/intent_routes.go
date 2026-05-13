package middleware

import (
	"net/http"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

// DefaultRouteIntents is the authoritative registry of
// (method, gin route) -> ActionIntent for the orchestration API.
//
// Every route registered via api.RegisterHandlersWithOptions must appear
// here (or in IntentExemptRoutes). Adding a new endpoint to api.gen.go
// without adding it here causes a startup panic via
// ValidateAllRoutesIntentsDeclared, so this map is impossible to drift from the
// generated routing table.
//
// Intent semantics (see auth/intent.go for the full taxonomy):
//
//	IntentView    — read-only; allowed when team is blocked
//	IntentCreate  — provisions new compute / storage; denied when blocked
//	IntentMutate  — changes existing resource lifetime/state; denied when blocked
//	IntentDelete  — frees an existing resource; allowed when blocked
var DefaultRouteIntents = RouteIntents{
	http.MethodGet: {
		// Sandboxes
		"/sandboxes":                     auth.IntentView,
		"/v2/sandboxes":                  auth.IntentView,
		"/sandboxes/metrics":             auth.IntentView,
		"/sandboxes/:sandboxID":          auth.IntentView,
		"/sandboxes/:sandboxID/logs":     auth.IntentView,
		"/v2/sandboxes/:sandboxID/logs":  auth.IntentView,
		"/sandboxes/:sandboxID/metrics":  auth.IntentView,
		"/snapshots":                     auth.IntentView,

		// Templates
		"/templates":                                      auth.IntentView,
		"/templates/:templateID":                          auth.IntentView,
		"/templates/:templateID/tags":                     auth.IntentView,
		"/templates/:templateID/files/:hash":              auth.IntentMutate,
		"/templates/aliases/:alias":                       auth.IntentView,
		"/templates/:templateID/builds/:buildID/logs":     auth.IntentView,
		"/templates/:templateID/builds/:buildID/status":   auth.IntentView,

		// Teams (user-scoped reads)
		"/teams":                       auth.IntentView,
		"/teams/:teamID/metrics":       auth.IntentView,
		"/teams/:teamID/metrics/max":   auth.IntentView,

		// Volumes
		"/volumes":             auth.IntentView,
		"/volumes/:volumeID":   auth.IntentView,

		// API keys (team-scoped reads)
		"/api-keys": auth.IntentView,

		// Admin (AdminTokenAuth — no team in context; middleware no-ops)
		"/nodes":          auth.IntentView,
		"/nodes/:nodeID":  auth.IntentView,
	},
	http.MethodPost: {
		// Sandboxes
		"/sandboxes":                          auth.IntentCreate,
		"/sandboxes/:sandboxID/pause":         auth.IntentMutate,
		"/sandboxes/:sandboxID/resume":        auth.IntentMutate,
		"/sandboxes/:sandboxID/connect":       auth.IntentMutate,
		"/sandboxes/:sandboxID/timeout":       auth.IntentMutate,
		"/sandboxes/:sandboxID/refreshes":     auth.IntentMutate,
		"/sandboxes/:sandboxID/snapshots":     auth.IntentCreate,

		// Templates
		"/templates":                                  auth.IntentCreate,
		"/v2/templates":                               auth.IntentCreate,
		"/v3/templates":                               auth.IntentCreate,
		"/templates/:templateID":                      auth.IntentMutate,
		"/templates/tags":                             auth.IntentMutate,
		"/templates/:templateID/builds/:buildID":      auth.IntentMutate,
		"/v2/templates/:templateID/builds/:buildID":   auth.IntentMutate,

		// Volumes
		"/volumes": auth.IntentCreate,

		// API keys (team-scoped)
		"/api-keys": auth.IntentMutate,

		// Access tokens (user-scoped — middleware no-ops on missing team)
		"/access-tokens": auth.IntentMutate,

		// Admin (AdminTokenAuth — no team in context; middleware no-ops)
		"/nodes/:nodeID":                       auth.IntentMutate,
		"/admin/teams/:teamID/builds/cancel":   auth.IntentMutate,
		"/admin/teams/:teamID/sandboxes/kill":  auth.IntentMutate,
	},
	http.MethodPatch: {
		"/templates/:templateID":     auth.IntentMutate,
		"/v2/templates/:templateID":  auth.IntentMutate,
		"/api-keys/:apiKeyID":        auth.IntentMutate,
	},
	http.MethodPut: {
		"/sandboxes/:sandboxID/network": auth.IntentMutate,
	},
	http.MethodDelete: {
		"/sandboxes/:sandboxID":           auth.IntentDelete,
		"/templates/:templateID":          auth.IntentDelete,
		"/templates/tags":                 auth.IntentMutate,
		"/volumes/:volumeID":              auth.IntentDelete,
		"/api-keys/:apiKeyID":             auth.IntentDelete,
		"/access-tokens/:accessTokenID":   auth.IntentDelete,
	},
}
