package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/hyperloop"
)

func (h *APIStore) Me(c *gin.Context) {
	ctx := c.Request.Context()
	sbx, err := h.findSandbox(c)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Error when finding source sandbox")
		h.logger.Error(ctx, "error finding sandbox for source addr", zap.String("addr", c.Request.RemoteAddr), zap.Error(err))

		return
	}

	c.JSON(http.StatusOK, &api.Me{SandboxID: sbx.Runtime.SandboxID})
}
