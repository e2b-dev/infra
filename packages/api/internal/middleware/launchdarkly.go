package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func InitLaunchDarklyContext(c *gin.Context) {
	// generate contexts where appropriate
	var contexts []ldcontext.Context
	if context, ok := createLaunchDarklyTeamContext(c); ok {
		contexts = append(contexts, context)
	}
	if context, ok := createLaunchDarklyUserContext(c); ok {
		contexts = append(contexts, context)
	}

	// store the context in ctx
	ctx := c.Request.Context()
	ctx = featureflags.SetContext(ctx, contexts...)
	c.Request = c.Request.WithContext(ctx)

	// we're done, move on
	c.Next()
}

func createLaunchDarklyUserContext(c *gin.Context) (ldcontext.Context, bool) {
	userID, err := auth.GetUserID(c)
	if err != nil {
		return ldcontext.Context{}, false
	}

	return featureflags.UserContext(userID.String()), true
}

func createLaunchDarklyTeamContext(c *gin.Context) (ldcontext.Context, bool) {
	authTeamInfo, err := auth.GetTeamInfo(c)
	if err != nil {
		return ldcontext.Context{}, false
	}

	var contexts []ldcontext.Context
	if team := authTeamInfo.Team; team != nil {
		contexts = append(contexts, featureflags.TeamContext(team.ID.String(), team.Name))
		if clusterID := team.ClusterID; clusterID != nil {
			contexts = append(contexts, featureflags.ClusterContext(clusterID.String()))
		}
	}

	if tier := authTeamInfo.Tier; tier != nil {
		contexts = append(contexts, featureflags.TierContext(tier.ID, tier.Name))
	}

	if len(contexts) == 0 {
		return ldcontext.Context{}, false
	}

	return ldcontext.NewMulti(contexts...), true
}
