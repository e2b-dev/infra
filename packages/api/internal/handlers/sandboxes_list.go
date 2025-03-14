package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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

type SandboxesListFilter struct {
	Query *string
}

func generateCursor(sandbox api.ListedSandbox) string {
	// Format: timestamp__sandboxID
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

func (a *APIStore) getRunningSandboxes(runningSandboxes []*instance.InstanceInfo, query *string) ([]api.ListedSandbox, error) {
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
	if query != nil {
		filters, err := parseFilters(*query)
		if err != nil {
			return nil, fmt.Errorf("error when parsing filters: %w", err)
		}

		filteredSandboxes, err := filterSandboxes(sandboxes, filters)
		if err != nil {
			return nil, fmt.Errorf("error when filtering sandboxes: %w", err)
		}

		sandboxes = filteredSandboxes
	}

	return sandboxes, nil
}

func (a *APIStore) getPausedSandboxes(ctx context.Context, teamID uuid.UUID, runningSandboxesIDs []string, query *string, limit *int32, cursorTime *time.Time, cursorID *string) ([]api.ListedSandbox, error) {
	sandboxes := make([]api.ListedSandbox, 0)

	var filters *map[string]string
	if query != nil {
		parsedFilters, err := parseFilters(*query)
		if err != nil {
			return nil, fmt.Errorf("error when parsing filters: %w", err)
		}

		filters = &parsedFilters
	}

	// Use default limit if not provided
	effectiveLimit := int32(100)
	if limit != nil {
		effectiveLimit = *limit
	}

	// Use the new cursor-based pagination function
	snapshots, err := a.db.GetTeamSnapshotsWithCursor(ctx, teamID, runningSandboxesIDs, effectiveLimit, filters, cursorTime, cursorID)
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

		memoryMB := int32(-1)
		cpuCount := int32(-1)

		memoryMB = int32(snapshotBuilds[0].RAMMB)
		cpuCount = int32(snapshotBuilds[0].Vcpu)

		sandbox := api.ListedSandbox{
			ClientID:   "00000000", // for backwards compatibility we need to return a client id
			TemplateID: env.ID,
			SandboxID:  snapshot.SandboxID,
			StartedAt:  snapshot.SandboxStartedAt,
			CpuCount:   cpuCount,
			MemoryMB:   memoryMB,
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

func (a *APIStore) getSandboxes(ctx context.Context, teamID uuid.UUID, params SandboxesListParams, limit *int32, nextToken *string) ([]api.ListedSandbox, error) {
	// Initialize empty slice for results
	sandboxes := make([]api.ListedSandbox, 0)

	// Parse cursor if provided
	var cursorTime *time.Time
	var cursorID *string
	if nextToken != nil && *nextToken != "" {
		parsedTime, parsedID, err := parseCursor(*nextToken)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor: %w", err)
		}
		cursorTime = &parsedTime
		cursorID = &parsedID
	}

	// Get all sandbox instances
	runningSandboxes := a.orchestrator.GetSandboxes(ctx, &teamID)

	// Running Sandbox IDs
	runningSandboxesIDs := make([]string, 0)
	for _, info := range runningSandboxes {
		runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.Instance.SandboxID))
	}

	// If we're requesting both running and paused sandboxes (or neither is specified),
	// we need to handle pagination carefully to ensure consistent ordering
	if params.State == nil || (slices.Contains(*params.State, api.Running) && slices.Contains(*params.State, api.Paused)) {
		// Get all running sandboxes
		runningSandboxList, err := a.getRunningSandboxes(runningSandboxes, params.Query)
		if err != nil {
			return nil, fmt.Errorf("error getting running sandboxes: %w", err)
		}

		// Get paused sandboxes with cursor-based pagination
		// We request limit+1 to check if there are more results
		effectiveLimit := *limit
		if len(runningSandboxList) < int(effectiveLimit) {
			// If we have fewer running sandboxes than the limit, we can request more paused sandboxes
			effectiveLimit = effectiveLimit - int32(len(runningSandboxList))
		} else {
			// If we have more running sandboxes than the limit, we don't need to request any paused sandboxes
			effectiveLimit = 0
		}

		pausedSandboxList, err := a.getPausedSandboxes(ctx, teamID, runningSandboxesIDs, params.Query, &effectiveLimit, cursorTime, cursorID)
		if err != nil {
			return nil, fmt.Errorf("error getting paused sandboxes: %w", err)
		}

		// Combine the results
		sandboxes = append(sandboxes, runningSandboxList...)
		sandboxes = append(sandboxes, pausedSandboxList...)

		return sandboxes, nil
	}

	// If we're only requesting running sandboxes
	if params.State != nil && slices.Contains(*params.State, api.Running) {
		runningSandboxList, err := a.getRunningSandboxes(runningSandboxes, params.Query)
		if err != nil {
			return nil, fmt.Errorf("error getting running sandboxes: %w", err)
		}
		sandboxes = append(sandboxes, runningSandboxList...)
	}

	// If we're only requesting paused sandboxes
	if params.State != nil && slices.Contains(*params.State, api.Paused) {
		pausedSandboxList, err := a.getPausedSandboxes(ctx, teamID, runningSandboxesIDs, params.Query, limit, cursorTime, cursorID)
		if err != nil {
			return nil, fmt.Errorf("error getting paused sandboxes: %w", err)
		}
		sandboxes = append(sandboxes, pausedSandboxList...)
	}

	return sandboxes, nil
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

	// Ensure we have a valid limit
	limit := int32(100) // Default limit
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
	}

	// Get sandboxes with pagination
	sandboxes, err := a.getSandboxes(ctx, team.ID, SandboxesListParams{
		State: params.State,
		Query: params.Query,
	}, &limit, params.NextToken)
	if err != nil {
		zap.L().Error("Error fetching sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning sandboxes for team '%s': %s", team.ID, err))
		return
	}

	// Sort sandboxes by descending started_at, then by sandbox ID for stability
	slices.SortFunc(sandboxes, func(a, b api.ListedSandbox) int {
		// First compare by timestamp (newest first)
		timeCompare := b.StartedAt.Compare(a.StartedAt)
		if timeCompare != 0 {
			return timeCompare
		}
		// If timestamps are equal, sort by sandbox ID (lexicographically)
		if a.SandboxID < b.SandboxID {
			return -1
		} else if a.SandboxID > b.SandboxID {
			return 1
		}
		return 0
	})

	// Apply limit and determine if there are more results
	var nextToken *string
	if len(sandboxes) > int(limit) {
		// We have more results than the limit, so we need to set the nextToken
		lastSandbox := sandboxes[limit-1]
		cursor := generateCursor(lastSandbox)
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
