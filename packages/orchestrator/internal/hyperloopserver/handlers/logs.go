package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (h *APIStore) Logs(c *gin.Context) {
	sbx, err := h.findSandbox(c)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Error when finding source sandbox")
		telemetry.ReportError(c, fmt.Sprintf("error finding sandbox for source addr %s", c.Request.RemoteAddr), err)
		return
	}

	sbxID := sbx.Runtime.SandboxID

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.sendAPIStoreError(c, http.StatusBadRequest, "Invalid body for logs")
		telemetry.ReportError(c, "error when parsing sandbox logs request", err, telemetry.WithSandboxID(sbxID))
		return
	}

	// Overwrite instanceID to ensure logs are from the correct sandbox
	payload["instanceID"] = sbx.Runtime.SandboxID

	logs, err := json.Marshal(payload)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when parsing logs payload")
		telemetry.ReportError(c, "error when parsing logs payload", err, telemetry.WithSandboxID(sbxID))
		return
	}

	request, err := http.NewRequestWithContext(c, http.MethodPost, h.collectorAddr, bytes.NewBuffer(logs))
	if err != nil {
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when creating request to forwarding sandbox logs")
		telemetry.ReportError(c, "error when creating forwarding sandbox logs", err, telemetry.WithSandboxID(sbxID))
		return
	}

	request.Header.Set("Content-Type", "application/json")
	response, err := h.collectorClient.Do(request)
	if err != nil {
		h.sendAPIStoreError(c, http.StatusInternalServerError, "Error when forwarding sandbox logs")
		telemetry.ReportError(c, "error when forwarding sandbox logs", err, telemetry.WithSandboxID(sbxID))
		return
	}
	defer response.Body.Close()

	c.Status(http.StatusOK)
}
