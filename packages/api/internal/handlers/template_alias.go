package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
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

	result, err := a.sqlcDB.GetTemplateAliasByAlias(ctx, alias)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.sendAPIStoreError(c, http.StatusNotFound, "Template alias not found")

			return
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting template alias")
		telemetry.ReportCriticalError(ctx, "error when getting template alias", err)

		return
	}

	// Team is not alias owner so we are returning forbidden
	// We don't want to return not found here as this endpoint is used for alias existence even for non owners
	if result.TeamID != team.ID {
		a.sendAPIStoreError(c, http.StatusForbidden, "You don't have access to this template alias")

		return
	}

	// Team is alias owner
	c.JSON(
		http.StatusOK, api.TemplateAliasResponse{
			Public:     result.Public,
			TemplateID: result.EnvID,
		},
	)
}
