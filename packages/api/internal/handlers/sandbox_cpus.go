package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDCpus(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDCpusJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	sbx, err := a.orchestrator.GetInstance(ctx, sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("sandbox \"%s\" doesn't exist or you don't have access to it", sandboxID))
		telemetry.ReportError(ctx, "error getting sandbox instance", err)

		return
	}

	if body.OnlineCpus < 1 {
		a.sendAPIStoreError(c, http.StatusBadRequest, "online_cpus cannot be less than 1")
		telemetry.ReportError(ctx, "online_cpus cannot be less than 1", nil)

		return
	}

	if int64(body.OnlineCpus) > sbx.VCpu {
		a.sendAPIStoreError(c, http.StatusBadRequest, "online_cpus cannot be more than the sandbox vcpu count")
		telemetry.ReportError(ctx, "online_cpus cannot be more than the sandbox vcpu count", nil)

		return
	}

	apiErr := a.orchestrator.UpdateSandbox(ctx, sandboxID, nil, &body.OnlineCpus, sbx.ClusterID, sbx.NodeID)
	if apiErr != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error setting online cpus on sandbox \"%s\"", sandboxID))
		telemetry.ReportError(ctx, "error when setting online cpus", apiErr)

		return
	}

	c.Status(http.StatusNoContent)
}
