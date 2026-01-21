package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
)

func (a *APIStore) PostSandboxesSandboxIDTimeout(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	team := c.Value(auth.TeamContextKey).(*types.Team)

	var duration time.Duration

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDTimeoutJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err), err)

		return
	}

	if body.Timeout < 0 {
		duration = 0
	} else {
		duration = time.Duration(body.Timeout) * time.Second
	}

	sandboxData, err := a.orchestrator.GetSandbox(ctx, sandboxID)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusNotFound, "Sandbox not found", nil)

		return
	}

	if sandboxData.TeamID != team.ID {
		a.sendAPIStoreError(ctx, c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox \"%s\"", sandboxID), nil)

		return
	}

	apiErr := a.orchestrator.KeepAliveFor(ctx, sandboxID, duration, true)
	if apiErr != nil {
		a.sendAPIStoreError(ctx, c, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	c.Status(http.StatusNoContent)
}
