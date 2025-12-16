package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (h *APIStore) Logs(c *gin.Context) {
	ctx := c.Request.Context()
	sbx, err := h.sandboxes.GetByHostPort(c.Request.RemoteAddr)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Error when finding source sandbox")
		h.logger.Error(ctx, "error finding sandbox for source addr", zap.String("addr", c.Request.RemoteAddr), zap.Error(err))

		return
	}

	sbxID := sbx.Runtime.SandboxID

	payload := make(map[string]any)
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Invalid body for logs")
		h.logger.Error(ctx, "error when parsing sandbox logs request", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}

	// Overwrite instanceID and teamID to avoid spoofing
	payload["instanceID"] = sbxID
	payload["teamID"] = sbx.Runtime.TeamID

	logs, err := json.Marshal(payload)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when parsing logs payload")
		h.logger.Error(ctx, "error when parsing logs payload", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}

	request, err := http.NewRequestWithContext(c, http.MethodPost, h.collectorAddr, bytes.NewBuffer(logs))
	if err != nil {
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when creating request to forwarding sandbox logs")
		h.logger.Error(ctx, "error when creating request to forwarding sandbox logs", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}

	request.Header.Set("Content-Type", "application/json")
	response, err := h.collectorClient.Do(request)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when forwarding sandbox logs")
		h.logger.Error(ctx, "error when forwarding sandbox logs", zap.Error(err), logger.WithSandboxID(sbxID))

		return
	}
	defer response.Body.Close()

	c.Status(http.StatusOK)
}
