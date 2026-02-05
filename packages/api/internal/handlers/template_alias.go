package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetTemplatesAliasesAlias(c *gin.Context, alias string) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeam(ctx, c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)

		return
	}

	identifier, _, err := id.ParseName(alias)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid alias format: %s", err))
		telemetry.ReportError(ctx, "invalid alias format", err)

		return
	}

	if err := id.ValidateNamespaceMatchesTeam(identifier, team.Slug); err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())

		return
	}

	aliasInfo, err := a.templateCache.ResolveAlias(ctx, identifier, team.Slug)
	if err != nil {
		apiErr := templatecache.ErrorToAPIError(err, identifier)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}

	// Ownership verification (handles edge case where template was transferred)
	if aliasInfo.TeamID != team.ID {
		a.sendAPIStoreError(c, http.StatusForbidden, "You don't have access to this template alias")

		return
	}

	// Team is alias owner
	c.JSON(
		http.StatusOK, api.TemplateAliasResponse{
			Public:     aliasInfo.Public,
			TemplateID: aliasInfo.TemplateID,
		},
	)
}
