package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PutSandboxesSandboxIDNetwork(c *gin.Context, sandboxID string) {
	ctx := c.Request.Context()

	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	team := auth.MustGetTeamInfo(c)

	body, err := ginutils.ParseBody[api.PutSandboxesSandboxIDNetworkJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	var allowedEntries []string
	if body.AllowOut != nil {
		allowedEntries = *body.AllowOut
	}

	var deniedEntries []string
	if body.DenyOut != nil {
		deniedEntries = *body.DenyOut
	}

	if apiErr := validateEgressRules(allowedEntries, deniedEntries); apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	if body.Rules != nil {
		sbxInfo, err := a.orchestrator.GetSandbox(ctx, team.ID, sandboxID)
		if err != nil {
			if errors.Is(err, sandbox.ErrNotFound) {
				a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))
			} else {
				telemetry.ReportError(ctx, "error getting sandbox for network update", err)
				a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to get sandbox")
			}

			return
		}

		if apiErr := validateNetworkRules(ctx, a.featureFlags, team.ID, sbxInfo.EnvdVersion, body.Rules); apiErr != nil {
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}
	}

	rules := apiRulesToDBRules(body.Rules)

	if apiErr := a.orchestrator.UpdateSandboxNetworkConfig(ctx, team.ID, sandboxID, allowedEntries, deniedEntries, rules, body.AllowInternetAccess); apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error updating sandbox network config", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	if len(rules) > 0 {
		domains := make([]string, 0, len(rules))
		for domain := range rules {
			domains = append(domains, domain)
		}

		a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "sandbox with network transform rules updated",
			a.posthog.GetPackageToPosthogProperties(&c.Request.Header).
				Set("sandbox_id", sandboxID).
				Set("domains", domains),
		)
	}

	c.Status(http.StatusNoContent)
}
