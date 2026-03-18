package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (a *APIStore) PutSandboxesSandboxIDNetwork(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()

	var err error
	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	team := auth.MustGetTeamInfo(c)

	body, err := utils.ParseBody[api.PutSandboxesSandboxIDNetworkJSONBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	egressUpdate := &types.SandboxNetworkEgressConfig{
		Off:              sharedutils.DerefOrDefault(body.EgressOff, false),
		AllowedAddresses: sharedutils.DerefOrDefault(body.AllowOut, nil),
		DeniedAddresses:  sharedutils.DerefOrDefault(body.DenyOut, nil),
	}

	ingressUpdate := &types.SandboxNetworkIngressConfig{
		Off:              sharedutils.DerefOrDefault(body.IngressOff, false),
		MaskRequestHost:  body.MaskRequestHost,
		AllowedAddresses: sharedutils.DerefOrDefault(body.AllowIn, nil),
		DeniedAddresses:  sharedutils.DerefOrDefault(body.DenyIn, nil),
	}

	if egressUpdate.Off && (len(egressUpdate.AllowedAddresses) > 0 || len(egressUpdate.DeniedAddresses) > 0) {
		a.sendAPIStoreError(c, http.StatusBadRequest, "egressOff cannot be set together with allowOut or denyOut")

		return
	}

	if !egressUpdate.Off {
		if apiErr := validateEgressRules(egressUpdate.AllowedAddresses, egressUpdate.DeniedAddresses); apiErr != nil {
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}
	}

	if ingressUpdate.Off && (len(ingressUpdate.AllowedAddresses) > 0 || len(ingressUpdate.DeniedAddresses) > 0) {
		a.sendAPIStoreError(c, http.StatusBadRequest, "ingressOff cannot be set together with allowIn or denyIn")

		return
	}

	if !ingressUpdate.Off {
		if apiErr := validateIngressRules(ingressUpdate.AllowedAddresses, ingressUpdate.DeniedAddresses); apiErr != nil {
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}
	}

	if apiErr := a.orchestrator.UpdateSandboxNetworkConfig(ctx, team.ID, sandboxID, egressUpdate, ingressUpdate); apiErr != nil {
		telemetry.ReportErrorByCode(ctx, apiErr.Code, "error updating sandbox network config", apiErr.Err)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	c.Status(http.StatusNoContent)
}
