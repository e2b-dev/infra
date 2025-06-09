package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1PauseSandbox(c *gin.Context, sandboxId api.SandboxId) {
	ctx := c.Request.Context()

	_, err := parseBody[api.V1PauseSandboxJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	panic("implement me")
}

func (a *APIStore) V1UpdateSandbox(c *gin.Context, sandboxId api.SandboxId) {
	ctx := c.Request.Context()

	_, err := parseBody[api.V1UpdateSandboxJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	panic("implement me")
}

func (a *APIStore) V1ListSandboxes(c *gin.Context, params api.V1ListSandboxesParams) {
	//TODO implement me
	panic("implement me")
}
