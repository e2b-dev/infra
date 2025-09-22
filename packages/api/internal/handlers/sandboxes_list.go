package handlers

import (
	"context"
	"fmt"
	"net/http"
	"slices"
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
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	maxSandboxListLimit     int32 = 100
	defaultSandboxListLimit int32 = 100
)

func (a *APIStore) getPausedSandboxes(ctx context.Context, teamID uuid.UUID, runningSandboxesIDs []string, metadataFilter *map[string]string, limit int32, cursorTime time.Time, cursorID string) ([]utils.PaginatedSandbox, error) {
	// Apply limit + 1 to check if there are more results
	queryLimit := limit + 1
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
		return nil, fmt.Errorf("error getting team snapshots: %w", err)
	}

	sandboxes := snapshotsToPaginatedSandboxes(snapshots)
	return sandboxes, nil
}

func getRunningSandboxes(runningSandboxes []instance.Data, metadataFilter *map[string]string) []utils.PaginatedSandbox {
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

	sandboxes := a.orchestrator.GetSandboxes(ctx, &team.ID, []instance.State{instance.StateRunning})
	runningSandboxes := getRunningSandboxes(sandboxes[instance.StateRunning], metadataFilter)

	// Sort sandboxes by start time descending
	utils.SortPaginatedSandboxesDesc(runningSandboxes)

	c.JSON(http.StatusOK, runningSandboxes)
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

	// Clip limit to max
	if limit > maxSandboxListLimit {
		limit = maxSandboxListLimit
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

	sandboxesInCache := a.orchestrator.GetSandboxes(ctx, &team.ID, []instance.State{instance.StateRunning, instance.StatePausing})

	if slices.Contains(states, api.Running) {
		runningSandboxList := instanceInfoToPaginatedSandboxes(sandboxesInCache[instance.StateRunning])

		// Filter based on metadata
		runningSandboxList = utils.FilterSandboxesOnMetadata(runningSandboxList, metadataFilter)

		// Set the total (before we apply the limit, but already with all filters)
		c.Header("X-Total-Running", strconv.Itoa(len(runningSandboxList)))

		// Filter based on cursor
		runningSandboxList = utils.FilterBasedOnCursor(runningSandboxList, cursorTime, cursorID)

		sandboxes = append(sandboxes, runningSandboxList...)
	}

	if slices.Contains(states, api.Paused) {
		// Running Sandbox IDs
		runningSandboxesIDs := make([]string, 0)
		for _, info := range sandboxesInCache[instance.StateRunning] {
			runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.SandboxID))
		}
		pausing := sandboxesInCache[instance.StatePausing]
		for _, info := range pausing {
			runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.SandboxID))
		}

		pausedSandboxList, err := a.getPausedSandboxes(ctx, team.ID, runningSandboxesIDs, metadataFilter, limit, cursorTime, cursorID)
		if err != nil {
			zap.L().Error("Error getting paused sandboxes", zap.Error(err))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting paused sandboxes")

			return
		}

		pausingSandboxList := instanceInfoToPaginatedSandboxes(pausing)
		pausingSandboxList = utils.FilterSandboxesOnMetadata(pausingSandboxList, metadataFilter)
		pausingSandboxList = utils.FilterBasedOnCursor(pausingSandboxList, cursorTime, cursorID)

		sandboxes = append(sandboxes, pausedSandboxList...)
		sandboxes = append(sandboxes, pausingSandboxList...)
	}

	// We need to sort again after merging running and paused sandboxes
	utils.SortPaginatedSandboxesDesc(sandboxes)

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
			zap.L().Error("disk size is not set for the sandbox", logger.WithSandboxID(snapshot.SandboxID))
		}

		envdVersion := ""
		if build.EnvdVersion != nil {
			envdVersion = *build.EnvdVersion
		} else {
			zap.L().Error("envd version is not set for the sandbox", logger.WithSandboxID(snapshot.SandboxID))
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
			PaginationTimestamp: snapshot.SandboxStartedAt.Time,
		}

		if snapshot.Metadata != nil {
			meta := api.SandboxMetadata(snapshot.Metadata)
			sandbox.Metadata = &meta
		}

		sandboxes = append(sandboxes, sandbox)
	}

	return sandboxes
}

func instanceInfoToPaginatedSandboxes(runningSandboxes []instance.Data) []utils.PaginatedSandbox {
	sandboxes := make([]utils.PaginatedSandbox, 0)

	// Add running sandboxes to results
	for _, info := range runningSandboxes {
		state := api.Running
		// If the sandbox is pausing, for the user it behaves the like a paused sandbox - it can be resumed, etc.
		if info.State == instance.StatePausing {
			state = api.Paused
		}

		sandbox := utils.PaginatedSandbox{
			ListedSandbox: api.ListedSandbox{
				ClientID:    info.ClientID,
				TemplateID:  info.BaseTemplateID,
				Alias:       info.Alias,
				SandboxID:   info.SandboxID,
				StartedAt:   info.StartTime,
				CpuCount:    api.CPUCount(info.VCpu),
				MemoryMB:    api.MemoryMB(info.RamMB),
				DiskSizeMB:  api.DiskSizeMB(info.TotalDiskSizeMB),
				EndAt:       info.EndTime,
				State:       state,
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
