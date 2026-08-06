package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) DeleteSnapshotsSnapshotID(c *gin.Context, rawSnapshotID api.SnapshotID) {
	ctx := c.Request.Context()
	teamInfo := auth.MustGetTeamInfo(c)
	teamID := teamInfo.Team.ID

	snapshotID, err := url.PathUnescape(rawSnapshotID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid snapshot ID")
		return
	}

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	identifier, _, err := id.ParseName(snapshotID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid snapshot ID: %s", err))
		return
	}

	if err := id.ValidateNamespaceMatchesTeam(identifier, teamInfo.Slug); err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())
		return
	}

	aliasInfo, resolveErr := a.templateCache.ResolveAlias(ctx, identifier, teamInfo.Slug)
	if resolveErr != nil {
		if errors.Is(resolveErr, templatecache.ErrTemplateNotFound) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Snapshot '%s' not found", snapshotID))
			return
		}
		apiErr := templatecache.ErrorToAPIError(resolveErr, identifier)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		return
	}

	if aliasInfo.TeamID != teamID {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Snapshot '%s' not found", snapshotID))
		return
	}

	templateID := aliasInfo.TemplateID

	aliasKeys, err := a.softDeleteTemplate(ctx, teamID, templateID)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error deleting snapshot template", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error deleting snapshot")
		return
	}
	if aliasKeys == nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Snapshot '%s' not found", snapshotID))
		return
	}

	a.templateCache.InvalidateAllTags(context.WithoutCancel(ctx), templateID)
	a.templateCache.InvalidateAliasesByTemplateID(context.WithoutCancel(ctx), templateID, aliasKeys)

	logger.L().Info(ctx, "Deleted snapshot template", logger.WithTemplateID(templateID), logger.WithTeamID(teamID.String()))

	c.Status(http.StatusNoContent)
}
