package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type SandboxesListParams struct {
	State *[]api.SandboxState
	Query *string
}

type SandboxListPaginationParams struct {
	Limit     *int32
	NextToken *string
}

func generateCursor(sandbox api.ListedSandbox) string {
	cursor := fmt.Sprintf("%s__%s", sandbox.StartedAt.Format(time.RFC3339Nano), sandbox.SandboxID)
	return base64.URLEncoding.EncodeToString([]byte(cursor))
}

func parseCursor(cursor string) (time.Time, string, error) {
	decoded, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("error decoding cursor: %w", err)
	}

	parts := strings.Split(string(decoded), "__")
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor format")
	}

	cursorTime, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid timestamp format in cursor: %w", err)
	}

	return cursorTime, parts[1], nil
}

func (a *APIStore) getRunningSandboxes(runningSandboxes []*instance.InstanceInfo, metadataFilter *map[string]string, cursorTime time.Time, cursorID string, limit *int32) ([]api.ListedSandbox, error) {
	sandboxes := make([]api.ListedSandbox, 0)

	// Get build IDs for running sandboxes
	buildIDs := make([]uuid.UUID, 0)
	for _, info := range runningSandboxes {
		if info.BuildID != nil {
			buildIDs = append(buildIDs, *info.BuildID)
		}
	}

	// Add running sandboxes to results
	for _, info := range runningSandboxes {
		if info.BuildID == nil {
			continue
		}

		sandbox := api.ListedSandbox{
			ClientID:   info.Instance.ClientID,
			TemplateID: info.Instance.TemplateID,
			Alias:      info.Instance.Alias,
			SandboxID:  info.Instance.SandboxID,
			StartedAt:  info.StartTime,
			CpuCount:   api.CPUCount(info.VCpu),
			MemoryMB:   api.MemoryMB(info.RamMB),
			EndAt:      info.GetEndTime(),
			State:      api.Running,
		}

		if info.Metadata != nil {
			meta := api.SandboxMetadata(info.Metadata)
			sandbox.Metadata = &meta
		}

		sandboxes = append(sandboxes, sandbox)
	}

	// filter sandboxes by metadata
	if metadataFilter != nil {
		filteredSandboxes, err := filterSandboxes(sandboxes, *metadataFilter)
		if err != nil {
			return nil, fmt.Errorf("error when filtering sandboxes: %w", err)
		}

		sandboxes = filteredSandboxes
	}

	// Apply cursor-based filtering if cursor is provided
	var filteredSandboxes []api.ListedSandbox
	for _, sandbox := range sandboxes {
		// Take sandboxes with start time before cursor time OR
		// same start time but sandboxID greater than cursor ID (for stability)
		if sandbox.StartedAt.Before(cursorTime) ||
			(sandbox.StartedAt.Equal(cursorTime) && sandbox.SandboxID > cursorID) {
			filteredSandboxes = append(filteredSandboxes, sandbox)
		}
	}
	sandboxes = filteredSandboxes

	// Apply limit if provided (get limit + 1 for pagination if possible)
	if limit != nil && len(sandboxes) > int(*limit) {
		sandboxes = sandboxes[:int(*limit)+1]
	}

	return sandboxes, nil
}

func (a *APIStore) getPausedSandboxes(ctx context.Context, teamID uuid.UUID, runningSandboxesIDs []string, metadataFilter *map[string]string, limit *int32, cursorTime time.Time, cursorID string) ([]api.ListedSandbox, error) {
	sandboxes := make([]api.ListedSandbox, 0)
	snapshots, err := a.db.GetTeamSnapshotsWithCursor(ctx, teamID, runningSandboxesIDs, int(*limit), metadataFilter, cursorTime, cursorID)
	if err != nil {
		return nil, fmt.Errorf("error getting team snapshots: %s", err)
	}

	// Add snapshots to results
	for _, snapshot := range snapshots {
		env := snapshot.Edges.Env
		if env == nil {
			continue
		}

		snapshotBuilds := env.Edges.Builds
		if len(snapshotBuilds) == 0 {
			continue
		}

		sandbox := api.ListedSandbox{
			ClientID:   "00000000", // for backwards compatibility we need to return a client id
			TemplateID: env.ID,
			SandboxID:  snapshot.SandboxID,
			StartedAt:  snapshot.SandboxStartedAt,
			CpuCount:   int32(snapshotBuilds[0].Vcpu),
			MemoryMB:   int32(snapshotBuilds[0].RAMMB),
			EndAt:      snapshot.CreatedAt,
			State:      api.Paused,
		}

		if snapshot.Metadata != nil {
			meta := api.SandboxMetadata(snapshot.Metadata)
			sandbox.Metadata = &meta
		}

		sandboxes = append(sandboxes, sandbox)
	}

	return sandboxes, nil
}

func (a *APIStore) getSandboxes(ctx context.Context, teamID uuid.UUID, params SandboxesListParams, paginationParams SandboxListPaginationParams) ([]api.ListedSandbox, *string, error) {
	sandboxes := make([]api.ListedSandbox, 0)

	// Parse metadata filter (query) if provided
	var metadataFilter *map[string]string
	if params.Query != nil {
		parsedMetadataFilter, err := parseFilters(*params.Query)
		if err != nil {
			zap.L().Error("Error parsing query", zap.Error(err))
			return nil, nil, fmt.Errorf("error parsing query: %w", err)
		}

		metadataFilter = &parsedMetadataFilter
	}

	var parsedCursorTime time.Time = time.Now()
	var parsedCursorID string = "zzzzzzzzzzzzzzzzzzzz"
	if paginationParams.NextToken != nil && *paginationParams.NextToken != "" {
		cursorTime, cursorID, err := parseCursor(*paginationParams.NextToken)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid cursor: %w", err)
		}

		parsedCursorTime = cursorTime
		parsedCursorID = cursorID
	}

	// Get all sandbox instances
	runningSandboxes := a.orchestrator.GetSandboxes(ctx, &teamID)

	// Running Sandbox IDs
	runningSandboxesIDs := make([]string, 0)
	for _, info := range runningSandboxes {
		runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.Instance.SandboxID))
	}

	if params.State == nil || (slices.Contains(*params.State, api.Running) && slices.Contains(*params.State, api.Paused)) {
		runningSandboxList, err := a.getRunningSandboxes(runningSandboxes, metadataFilter, parsedCursorTime, parsedCursorID, paginationParams.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting running sandboxes: %w", err)
		}

		pausedSandboxList, err := a.getPausedSandboxes(ctx, teamID, runningSandboxesIDs, metadataFilter, paginationParams.Limit, parsedCursorTime, parsedCursorID)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting paused sandboxes: %w", err)
		}

		sandboxes = append(sandboxes, runningSandboxList...)
		sandboxes = append(sandboxes, pausedSandboxList...)
	} else if params.State != nil && slices.Contains(*params.State, api.Running) {
		runningSandboxList, err := a.getRunningSandboxes(runningSandboxes, metadataFilter, parsedCursorTime, parsedCursorID, paginationParams.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting running sandboxes: %w", err)
		}
		sandboxes = append(sandboxes, runningSandboxList...)
	} else if params.State != nil && slices.Contains(*params.State, api.Paused) {
		pausedSandboxList, err := a.getPausedSandboxes(ctx, teamID, runningSandboxesIDs, metadataFilter, paginationParams.Limit, parsedCursorTime, parsedCursorID)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting paused sandboxes: %w", err)
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
	if len(sandboxes) > int(*paginationParams.Limit) {
		// We have more results than the limit, so we need to set the nextToken
		lastSandbox := sandboxes[*paginationParams.Limit-1]
		cursor := generateCursor(lastSandbox)
		nextToken = &cursor

		// Trim to the requested limit
		sandboxes = sandboxes[:*paginationParams.Limit]
	}

	return sandboxes, nextToken, nil
}

func parseFilters(query string) (map[string]string, error) {
	query, err := url.QueryUnescape(query)
	if err != nil {
		return nil, fmt.Errorf("error when unescaping query: %w", err)
	}

	// Parse filters, both key and value are also unescaped
	filters := make(map[string]string)

	for _, filter := range strings.Split(query, "&") {
		parts := strings.Split(filter, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key value pair in query")
		}

		key, err := url.QueryUnescape(parts[0])
		if err != nil {
			return nil, fmt.Errorf("error when unescaping key: %w", err)
		}

		value, err := url.QueryUnescape(parts[1])
		if err != nil {
			return nil, fmt.Errorf("error when unescaping value: %w", err)
		}

		filters[key] = value
	}

	return filters, nil
}

func filterSandboxes(sandboxes []api.ListedSandbox, filters map[string]string) ([]api.ListedSandbox, error) {
	// Filter instances to match all filters
	n := 0
	for _, instance := range sandboxes {
		if instance.Metadata == nil {
			continue
		}

		matchesAll := true
		for key, value := range filters {
			if metadataValue, ok := (*instance.Metadata)[key]; !ok || metadataValue != value {
				matchesAll = false
				break
			}
		}

		if matchesAll {
			sandboxes[n] = instance
			n++
		}
	}

	// Trim slice
	sandboxes = sandboxes[:n]

	return sandboxes, nil
}

func (a *APIStore) GetSandboxes(c *gin.Context, params api.GetSandboxesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list running instances")

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed running instances", properties)

	// Get sandboxes with pagination
	sandboxes, nextToken, err := a.getSandboxes(ctx, team.ID, SandboxesListParams{
		State: params.State,
		Query: params.Query,
	}, SandboxListPaginationParams{
		Limit:     params.Limit,
		NextToken: params.NextToken,
	})
	if err != nil {
		zap.L().Error("Error fetching sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning sandboxes for team '%s': %s", team.ID, err))
		return
	}

	// Add pagination info to headers
	if nextToken != nil {
		c.Header("X-Next-Token", *nextToken)
	}
	c.Header("X-Total-Items", strconv.Itoa(len(sandboxes)))

	c.JSON(http.StatusOK, sandboxes)
}
