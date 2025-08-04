package handlers

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultSandboxListLimit int32 = 1000

func (a *APIStore) getPausedSandboxes(ctx context.Context, teamID uuid.UUID, runningSandboxesIDs []string, metadataFilter *map[string]string, limit int32, cursorTime time.Time, cursorID string) ([]utils.PaginatedSandbox, error) {
	// Apply limit + 1 to check if there are more results
	queryLimit := int32(limit) + 1
	queryMetadata := types.JSONBStringMap{}
	if metadataFilter != nil {
		queryMetadata = *metadataFilter
	}

	snapshots, err := a.sqlcDB.GetSnapshotsWithCursor(
		ctx, queries.GetSnapshotsWithCursorParams{
			Limit:                 queryLimit,
			TeamID:                teamID,
			Metadata:              queryMetadata,
			CursorTime:            pgtype.Timestamptz{Time: cursorTime, Valid: true},
			CursorID:              cursorID,
			SnapshotExcludeSbxIds: runningSandboxesIDs,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error getting team snapshots: %s", err)
	}

	sandboxes := snapshotsToPaginatedSandboxes(snapshots)
	return sandboxes, nil
}

func getRunningSandboxes(ctx context.Context, orchestrator *orchestrator.Orchestrator, teamID uuid.UUID, metadataFilter *map[string]string) []utils.PaginatedSandbox {
	// Get all sandbox instances
	runningSandboxes := orchestrator.GetSandboxes(ctx, &teamID)

	// Running Sandbox IDs
	runningSandboxList := instanceInfoToPaginatedSandboxes(runningSandboxes)

	// Filter sandboxes based on metadata
	runningSandboxList = utils.FilterSandboxesOnMetadata(runningSandboxList, metadataFilter)

	return runningSandboxList
}

func (a *APIStore) GetSandboxes(c *gin.Context, params api.GetSandboxesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list sandboxes")

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed sandboxes", properties)

	metadataFilter, err := utils.ParseMetadata(params.Metadata)
	if err != nil {
		zap.L().Error("Error parsing metadata", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error parsing metadata: %s", err))

		return
	}

	sandboxes := getRunningSandboxes(ctx, a.orchestrator, team.ID, metadataFilter)

	// Sort sandboxes by start time descending
	slices.SortFunc(sandboxes, func(a, b utils.PaginatedSandbox) int {
		// SortFunc sorts the list ascending by default, because we want the opposite behavior we switch `a` and `b`
		return b.StartedAt.Compare(a.StartedAt)
	})

	c.JSON(http.StatusOK, sandboxes)
}

func (a *APIStore) GetV2Sandboxes(c *gin.Context, params api.GetV2SandboxesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list sandboxes")

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed sandboxes", properties)

	// If no state is provided we want to return both running and paused sandboxes
	states := make([]api.SandboxState, 0)
	if params.State == nil {
		states = append(states, api.Running, api.Paused)
	} else {
		states = append(states, *params.State...)
	}

	limit := defaultSandboxListLimit
	if params.Limit != nil {
		limit = *params.Limit
	}

	metadataFilter, err := utils.ParseMetadata(params.Metadata)
	if err != nil {
		zap.L().Error("Error parsing metadata", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error parsing metadata")

		return
	}

	// Get sandboxes with pagination
	sandboxes := make([]utils.PaginatedSandbox, 0)

	// Parse the next token to offset sandboxes for pagination
	cursorTime, cursorID, err := utils.ParseNextToken(params.NextToken)
	if err != nil {
		zap.L().Error("Error parsing cursor", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid next token")

		return
	}

	// Get all sandbox instances
	runningSandboxes := a.orchestrator.GetSandboxes(ctx, &team.ID)

	// Running Sandbox IDs
	runningSandboxesIDs := make([]string, 0)
	for _, info := range runningSandboxes {
		runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.Instance.SandboxID))
	}

	if slices.Contains(states, api.Running) {
		runningSandboxList := instanceInfoToPaginatedSandboxes(runningSandboxes)

		// Filter based on metadata
		runningSandboxList = utils.FilterSandboxesOnMetadata(runningSandboxList, metadataFilter)

		// Filter based on cursor and limit
		runningSandboxList = utils.FilterBasedOnCursor(runningSandboxList, cursorTime, cursorID, limit)

		sandboxes = append(sandboxes, runningSandboxList...)
	}

	if slices.Contains(states, api.Paused) {
		pausedSandboxList, err := a.getPausedSandboxes(ctx, team.ID, runningSandboxesIDs, metadataFilter, limit, cursorTime, cursorID)
		if err != nil {
			zap.L().Error("Error getting paused sandboxes", zap.Error(err))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting paused sandboxes")

			return
		}
		sandboxes = append(sandboxes, pausedSandboxList...)
	}

	// Sort by StartedAt (descending), then by SandboxID (ascending) for stability
	sort.Slice(sandboxes, func(a, b int) bool {
		if !sandboxes[a].StartedAt.Equal(sandboxes[b].StartedAt) {
			return sandboxes[a].StartedAt.After(sandboxes[b].StartedAt)
		}
		return sandboxes[a].SandboxID < sandboxes[b].SandboxID
	})

	var nextToken *string
	if len(sandboxes) > int(limit) {
		// We have more results than the limit, so we need to set the nextToken
		lastSandbox := sandboxes[limit-1]
		cursor := lastSandbox.GenerateCursor()
		nextToken = &cursor

		// Trim to the requested limit
		sandboxes = sandboxes[:limit]
	}

	// Add pagination info to headers
	if nextToken != nil {
		c.Header("X-Next-Token", *nextToken)
	}

	c.Header("X-Total-Items", strconv.Itoa(len(sandboxes)))
	c.JSON(http.StatusOK, sandboxes)
}

func snapshotsToPaginatedSandboxes(snapshots []queries.GetSnapshotsWithCursorRow) []utils.PaginatedSandbox {
	sandboxes := make([]utils.PaginatedSandbox, 0)

	// Add snapshots to results
	for _, record := range snapshots {
		snapshot := record.Snapshot
		build := record.EnvBuild

		var alias *string
		if len(record.Aliases) > 0 {
			alias = &record.Aliases[0]
		}

		diskSize := int32(0)
		if build.TotalDiskSizeMb != nil {
			diskSize = int32(*build.TotalDiskSizeMb)
		} else {
			zap.L().Error("disk size is not set for the sandbox", zap.String("sandbox_id", snapshot.SandboxID))
		}

		envdVersion := ""
		if build.EnvdVersion != nil {
			envdVersion = *build.EnvdVersion
		} else {
			zap.L().Error("envd version is not set for the sandbox", zap.String("sandbox_id", snapshot.SandboxID))
		}

		sandbox := utils.PaginatedSandbox{
			ListedSandbox: api.ListedSandbox{
				ClientID:    consts.ClientID, // for backwards compatibility we need to return a client id
				Alias:       alias,
				TemplateID:  snapshot.BaseEnvID,
				SandboxID:   snapshot.SandboxID,
				StartedAt:   snapshot.SandboxStartedAt.Time,
				CpuCount:    int32(build.Vcpu),
				MemoryMB:    int32(build.RamMb),
				DiskSizeMB:  diskSize,
				EndAt:       snapshot.CreatedAt.Time,
				State:       api.Paused,
				EnvdVersion: envdVersion,
			},
			PaginationTimestamp: snapshot.CreatedAt.Time,
		}

		if snapshot.Metadata != nil {
			meta := api.SandboxMetadata(snapshot.Metadata)
			sandbox.Metadata = &meta
		}

		sandboxes = append(sandboxes, sandbox)
	}

	return sandboxes
}

func instanceInfoToPaginatedSandboxes(runningSandboxes []*instance.InstanceInfo) []utils.PaginatedSandbox {
	sandboxes := make([]utils.PaginatedSandbox, 0)

	// Add running sandboxes to results
	for _, info := range runningSandboxes {
		sandbox := utils.PaginatedSandbox{
			ListedSandbox: api.ListedSandbox{
				ClientID:    info.Instance.ClientID,
				TemplateID:  info.BaseTemplateID,
				Alias:       info.Instance.Alias,
				SandboxID:   info.Instance.SandboxID,
				StartedAt:   info.StartTime,
				CpuCount:    api.CPUCount(info.VCpu),
				MemoryMB:    api.MemoryMB(info.RamMB),
				DiskSizeMB:  api.DiskSizeMB(info.TotalDiskSizeMB),
				EndAt:       info.GetEndTime(),
				State:       api.Running,
				EnvdVersion: info.EnvdVersion,
			},
			PaginationTimestamp: info.StartTime,
		}

		if info.Metadata != nil {
			meta := api.SandboxMetadata(info.Metadata)
			sandbox.Metadata = &meta
		}

		sandboxes = append(sandboxes, sandbox)
	}

	return sandboxes
}
