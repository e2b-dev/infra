package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSnapshotsSnapshotID(c *gin.Context, rawSnapshotID api.SnapshotID) {
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

	identifier, tag, err := id.ParseName(snapshotID)
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
	tagStr := id.DefaultTag
	if tag != nil {
		tagStr = *tag
	}

	snapTemplates, dbErr := a.sqlcDB.ListTeamSnapshotTemplates(ctx, queries.ListTeamSnapshotTemplatesParams{
		TeamID:     teamID,
		EnvID:      &templateID,
		Tag:        &tagStr,
		CursorTime: time.Now().Add(24 * time.Hour),
		CursorID:   maxSnapshotTemplateID,
		PageLimit:  1,
	})
	if dbErr != nil {
		telemetry.ReportCriticalError(ctx, "error looking up snapshot template", dbErr)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error looking up snapshot")
		return
	}

	if len(snapTemplates) == 0 {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Snapshot '%s' not found", snapshotID))
		return
	}

	snap := snapTemplates[0]
	displayID := id.WithTag(snap.SnapshotID, snap.Tag)
	if len(snap.Names) > 0 {
		displayID = id.WithTag(snap.Names[0], snap.Tag)
	}

	diskMB := int32(0)
	if snap.TotalDiskSizeMb != nil {
		diskMB = int32(*snap.TotalDiskSizeMb)
	}
	c.JSON(http.StatusOK, api.SnapshotInfo{
		SnapshotID: displayID,
		Names:      snap.Names,
		CpuCount:   int32(snap.Vcpu),
		MemoryMB:   int32(snap.RamMb),
		DiskSizeMB: diskMB,
		CreatedAt:  snap.CreatedAt,
	})
}
