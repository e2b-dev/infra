package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (a *APIStore) PostAdminCachesCacheInvalidateObjectID(c *gin.Context, cache api.CacheType, objectID string) {
	switch cache {
	case api.Templates:
		a.templateCache.Invalidate(objectID)
	case api.Auth:
		a.authCache.Invalidate(objectID)
	case api.Sandboxes:
		a.instanceCache.Invalidate(objectID)
	case api.Builds:
		a.buildCache.Invalidate(objectID)
	}

	c.Status(http.StatusNoContent)
}
