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
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	sandboxesDefaultLimit = int32(100)
	sandboxesMaxLimit     = int32(100)
)

func (a *APIStore) getPausedSandboxes(ctx context.Context, teamID uuid.UUID, runningSandboxesIDs []string, metadataFilter *map[string]string, queryLimit int32, cursorTime time.Time, cursorID string) ([]utils.PaginatedSandbox, error) {
	queryMetadata := dbtypes.JSONBStringMap{}
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

	sandboxes := snapshotsToPaginatedSandboxes(ctx, snapshots)

	return sandboxes, nil
}

func getRunningSandboxes(runningSandboxes []sandbox.Sandbox, metadataFilter *map[string]string) []utils.PaginatedSandbox {
	// Running Sandbox IDs
	runningSandboxList := instanceInfoToPaginatedSandboxes(runningSandboxes)

	// Filter sandboxes based on metadata
	runningSandboxList = utils.FilterSandboxesOnMetadata(runningSandboxList, metadataFilter)

	return runningSandboxList
}

func (a *APIStore) GetSandboxes(c *gin.Context, params api.GetSandboxesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list sandboxes")

	teamInfo := c.Value(auth.TeamContextKey).(*types.Team)
	team := teamInfo.Team

	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "listed sandboxes", properties)

	metadataFilter, err := utils.ParseMetadata(ctx, params.Metadata)
	if err != nil {
		logger.L().Error(ctx, "Error parsing metadata", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error parsing metadata: %s", err))

		return
	}

	sandboxes, err := a.orchestrator.GetSandboxes(ctx, team.ID, []sandbox.State{sandbox.StateRunning})
	if err != nil {
		logger.L().Error(ctx, "Error getting sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandboxes")

		return
	}

	runningSandboxes := getRunningSandboxes(sandboxes, metadataFilter)

	// Sort sandboxes by start time descending
	utils.SortPaginatedSandboxesDesc(runningSandboxes)

	c.JSON(http.StatusOK, runningSandboxes)
}

func (a *APIStore) GetV2Sandboxes(c *gin.Context, params api.GetV2SandboxesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list sandboxes")

	teamInfo := c.Value(auth.TeamContextKey).(*types.Team)
	team := teamInfo.Team

	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "listed sandboxes", properties)

	// If no state is provided we want to return both running and paused sandboxes
	states := make([]api.SandboxState, 0)
	if params.State == nil {
		states = append(states, api.Running, api.Paused)
	} else {
		states = append(states, *params.State...)
	}

	// Initialize pagination
	pagination, err := utils.NewPagination[utils.PaginatedSandbox](
		utils.PaginationParams{
			Limit:     params.Limit,
			NextToken: params.NextToken,
		},
		utils.PaginationConfig{
			DefaultLimit: sandboxesDefaultLimit,
			MaxLimit:     sandboxesMaxLimit,
			DefaultID:    utils.MaxSandboxID,
		},
	)
	if err != nil {
		telemetry.ReportError(ctx, "error parsing pagination cursor", err)
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid next token")

		return
	}

	metadataFilter, err := utils.ParseMetadata(ctx, params.Metadata)
	if err != nil {
		logger.L().Error(ctx, "Error parsing metadata", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error parsing metadata")

		return
	}

	// Get sandboxes with pagination
	sandboxes := make([]utils.PaginatedSandbox, 0)

	allSandboxes, err := a.orchestrator.GetSandboxes(ctx, team.ID, []sandbox.State{sandbox.StateRunning, sandbox.StatePausing})
	if err != nil {
		logger.L().Error(ctx, "Error getting sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting sandboxes")

		return
	}

	runningSandboxes := sharedUtils.Filter(allSandboxes, func(sbx sandbox.Sandbox) bool {
		return sbx.State == sandbox.StateRunning
	})
	pausingSandboxes := sharedUtils.Filter(allSandboxes, func(sbx sandbox.Sandbox) bool {
		return sbx.State == sandbox.StatePausing
	})

	if slices.Contains(states, api.Running) {
		runningSandboxList := instanceInfoToPaginatedSandboxes(runningSandboxes)

		// Filter based on metadata
		runningSandboxList = utils.FilterSandboxesOnMetadata(runningSandboxList, metadataFilter)

		// Set the total (before we apply the limit, but already with all filters)
		c.Header("X-Total-Running", strconv.Itoa(len(runningSandboxList)))

		// Filter based on cursor
		runningSandboxList = utils.FilterBasedOnCursor(runningSandboxList, pagination.CursorTime(), pagination.CursorID())

		sandboxes = append(sandboxes, runningSandboxList...)
	}

	if slices.Contains(states, api.Paused) {
		// Running Sandbox IDs
		runningSandboxesIDs := make([]string, 0)
		for _, info := range runningSandboxes {
			runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.SandboxID))
		}
		for _, info := range pausingSandboxes {
			runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.SandboxID))
		}

		pausedSandboxList, err := a.getPausedSandboxes(ctx, team.ID, runningSandboxesIDs, metadataFilter, pagination.QueryLimit(), pagination.CursorTime(), pagination.CursorID())
		if err != nil {
			logger.L().Error(ctx, "Error getting paused sandboxes", zap.Error(err))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting paused sandboxes")

			return
		}

		pausingSandboxList := instanceInfoToPaginatedSandboxes(pausingSandboxes)
		pausingSandboxList = utils.FilterSandboxesOnMetadata(pausingSandboxList, metadataFilter)
		pausingSandboxList = utils.FilterBasedOnCursor(pausingSandboxList, pagination.CursorTime(), pagination.CursorID())

		sandboxes = append(sandboxes, pausedSandboxList...)
		sandboxes = append(sandboxes, pausingSandboxList...)
	}

	// We need to sort again after merging running and paused sandboxes
	utils.SortPaginatedSandboxesDesc(sandboxes)

	sandboxes = pagination.ProcessResultsWithHeader(c, sandboxes, func(s utils.PaginatedSandbox) (time.Time, string) {
		return s.PaginationTimestamp, s.SandboxID
	})

	c.JSON(http.StatusOK, sandboxes)
}

func snapshotsToPaginatedSandboxes(ctx context.Context, snapshots []queries.GetSnapshotsWithCursorRow) []utils.PaginatedSandbox {
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
			logger.L().Error(ctx, "disk size is not set for the sandbox", logger.WithSandboxID(snapshot.SandboxID))
		}

		envdVersion := ""
		if build.EnvdVersion != nil {
			envdVersion = *build.EnvdVersion
		} else {
			logger.L().Error(ctx, "envd version is not set for the sandbox", logger.WithSandboxID(snapshot.SandboxID))
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

func instanceInfoToPaginatedSandboxes(runningSandboxes []sandbox.Sandbox) []utils.PaginatedSandbox {
	sandboxes := make([]utils.PaginatedSandbox, 0)

	// Add running sandboxes to results
	for _, info := range runningSandboxes {
		state := api.Running
		// If the sandbox is pausing, for the user it behaves the like a paused sandbox - it can be resumed, etc.
		if info.State == sandbox.StatePausing {
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
