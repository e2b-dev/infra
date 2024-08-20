package handlers

import (
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/gin-gonic/gin"
)

func (a *APIStore) PostAdminCachesInvalidate(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.CacheInvalidationRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when parsing request")
		return
	}

	// Invalidate cache
	switch body.Cache {
	case api.Templates:
		a.templateCache.InvalidateAll()
	case api.Auth:
		a.authCache.InvalidateAll()
	case api.Sandboxes:
		a.instanceCache.InvalidateAll()
	case api.Builds:
		a.buildCache.InvalidateAll()
	case api.All:
		a.templateCache.InvalidateAll()
		a.authCache.InvalidateAll()
		a.instanceCache.InvalidateAll()
		a.buildCache.InvalidateAll()
	}

	c.Status(http.StatusNoContent)
}
