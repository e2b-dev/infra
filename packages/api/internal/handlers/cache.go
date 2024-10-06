package handlers

import (
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) PostAdminCachesCacheInvalidateObjectID(c *gin.Context, cache api.CacheType, objectID string) {
	ctx := c.Request.Context()

	switch cache {
	case api.Templates:
		a.templateCache.Invalidate(objectID)
	case api.Auth:
		a.authCache.Invalidate(objectID)
	case api.Sandboxes:
		a.instanceCache.Invalidate(objectID)
	case api.Builds:
		a.buildCache.Invalidate(objectID)
	case api.Aliases:
		a.templateCache.InvalidateAlias(objectID)
	}

	telemetry.ReportEvent(ctx, fmt.Sprintf("Invalidated cache for %s with id %s", cache, objectID))

	c.Status(http.StatusNoContent)
}
