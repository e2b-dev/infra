package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

const legacyTeamMutationDisabledMessage = "Legacy team mutations are no longer available. Use the workspace API."

var legacyTeamMutationRoutes = []RouteTemplate{
	{Method: http.MethodPost, Path: "/teams"},
	{Method: http.MethodPatch, Path: "/teams/:teamID"},
	{Method: http.MethodPost, Path: "/teams/:teamID/members"},
	{Method: http.MethodDelete, Path: "/teams/:teamID/members/:userId"},
	{Method: http.MethodPost, Path: "/admin/users/bootstrap"},
	{Method: http.MethodDelete, Path: "/admin/users/:userId"},
	{Method: http.MethodPost, Path: "/admin/teams/bootstrap"},
	{Method: http.MethodPut, Path: "/admin/teams/:teamID/cluster"},
	{Method: http.MethodDelete, Path: "/admin/teams/:teamID/cluster/:clusterID"},
}

func DisableLegacyTeamMutations(featureFlags *featureflags.Client) gin.HandlerFunc {
	return RejectRoutes(
		legacyTeamMutationRoutes,
		func(ctx context.Context) bool {
			return featureFlags.BoolFlag(ctx, featureflags.DisableLegacyTeamMutationsFlag)
		},
		RouteRejection{
			Reason:  "legacy_team_mutations_disabled",
			Status:  http.StatusPreconditionFailed,
			Message: legacyTeamMutationDisabledMessage,
		},
	)
}
