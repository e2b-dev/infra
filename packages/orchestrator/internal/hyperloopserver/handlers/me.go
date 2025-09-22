package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/hyperloop"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (h *APIStore) Me(c *gin.Context) {
	sbx, err := h.findSandbox(c)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Error when finding source sandbox")
		telemetry.ReportCriticalError(c, "error when parsing request", err)
		return
	}

	c.JSON(http.StatusOK, &api.Me{SandboxID: sbx.Runtime.SandboxID})
}
