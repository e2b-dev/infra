package middleware

import (
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.uber.org/zap"
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
	userID, ok := c.Value(auth.UserIDContextKey).(uuid.UUID)
	if !ok {
		return ldcontext.Context{}, false
	}

	return featureflags.UserContext(userID.String()), true
}

func createLaunchDarklyTeamContext(c *gin.Context) (ldcontext.Context, bool) {
	authTeamInfo, ok := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	if !ok {
		zap.L().Warn("Team Context not found in context")
		return ldcontext.Context{}, false
	}

	var contexts []ldcontext.Context
	if team := authTeamInfo.Team; team != nil {
		contexts = append(contexts, featureflags.TeamContext(team.ID.String(), team.Name))
		contexts = append(contexts, featureflags.ClusterContext(team.ClusterID.String()))
	}

	if tier := authTeamInfo.Tier; tier != nil {
		contexts = append(contexts, featureflags.TierContext(tier.ID, tier.Name))
	}

	if len(contexts) == 0 {
		return ldcontext.Context{}, false
	}

	return mergeContexts(contexts), true
}
