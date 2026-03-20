package handlers

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/hyperloopserver/contracts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (h *APIStore) Me(c *gin.Context) {
	ctx := c.Request.Context()
	sbx, err := h.sandboxes.GetByHostPort(c.Request.RemoteAddr)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Error when finding source sandbox")
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		h.logger.Error(ctx, "error finding sandbox for source addr", logger.WithSandboxIP(ip), zap.Error(err))

		return
	}

	c.JSON(http.StatusOK, &contracts.Me{SandboxID: sbx.Runtime.SandboxID})
}
