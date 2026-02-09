package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/hyperloopserver/contracts"
)

func (h *APIStore) Me(c *gin.Context) {
	ctx := c.Request.Context()
	sbx, err := h.sandboxes.GetByHostPort(c.Request.RemoteAddr)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Error when finding source sandbox")
		h.logger.Error(ctx, "error finding sandbox for source addr", zap.String("addr", c.Request.RemoteAddr), zap.Error(err))

		return
	}

	c.JSON(http.StatusOK, &contracts.Me{SandboxID: sbx.Runtime.SandboxID})
}
