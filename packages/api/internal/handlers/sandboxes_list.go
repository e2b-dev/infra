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

type SandboxesListPaginate struct {
	NextToken *string
	Limit     *int32
}

type SandboxesListResult struct {
	Sandboxes []api.ListedSandbox
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

func (a *APIStore) getPausedSandboxes(ctx context.Context, teamID uuid.UUID, query *string, runningSandboxesIDs []string, limit *int32) ([]api.ListedSandbox, error) {
	sandboxes := make([]api.ListedSandbox, 0)

	var filters *map[string]string
	if query != nil {
		parsedFilters, err := parseFilters(*query)
		if err != nil {
			return nil, fmt.Errorf("error when parsing filters: %w", err)
		}

		filters = &parsedFilters
	}

	snapshots, err := a.db.GetTeamSnapshots(ctx, teamID, runningSandboxesIDs, limit, filters)
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

func (a *APIStore) getSandboxes(ctx context.Context, teamID uuid.UUID, params SandboxesListParams, limit *int32) ([]api.ListedSandbox, error) {
	// Initialize empty slice for results
	sandboxes := make([]api.ListedSandbox, 0)

	// Get all sandbox instances
	runningSandboxes := a.orchestrator.GetSandboxes(ctx, &teamID)

	// Running Sandbox IDs
	runningSandboxesIDs := make([]string, 0)
	for _, info := range runningSandboxes {
		runningSandboxesIDs = append(runningSandboxesIDs, utils.ShortID(info.Instance.SandboxID))
	}

	// Only fetch running sandboxes if we need them (state is nil or "running")
	if params.State == nil || slices.Contains(*params.State, api.Running) {
		runningSandboxList, err := a.getRunningSandboxes(runningSandboxes, params.Query)
		if err != nil {
			return nil, fmt.Errorf("error getting running sandboxes: %w", err)
		}
		sandboxes = append(sandboxes, runningSandboxList...)
	}

	// Only fetch snapshots if we need them (state is nil or "paused")
	if params.State == nil || slices.Contains(*params.State, api.Paused) {
		pausedSandboxList, err := a.getPausedSandboxes(ctx, teamID, params.Query, runningSandboxesIDs, limit)
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

// Paginate sandboxes
func paginateSandboxes(sandboxes []api.ListedSandbox, paginate SandboxesListPaginate) (SandboxesListResult, error) {
	result := SandboxesListResult{
		Sandboxes: make([]api.ListedSandbox, 0),
		NextToken: nil,
	}

	// Sort sandboxes by descending created_at
	slices.SortFunc(sandboxes, func(a, b api.ListedSandbox) int {
		return b.StartedAt.Compare(a.StartedAt)
	})

	// If cursor is provided, find the starting position
	startIndex := 0
	if paginate.NextToken != nil && *paginate.NextToken != "" {
		cursorTime, cursorID, err := parseCursor(*paginate.NextToken)
		if err != nil {
			return result, fmt.Errorf("invalid cursor: %w", err)
		}

		// Find the sandbox that matches the cursor
		found := false
		for i, sandbox := range sandboxes {
			sandboxTime := sandbox.StartedAt
			if sandboxTime.Before(cursorTime) || (sandboxTime.Equal(cursorTime) && sandbox.SandboxID > cursorID) {
				startIndex = i
				found = true
				break
			}
		}

		if !found {
			startIndex = 0
		}
	}

	endIndex := startIndex + int(*paginate.Limit)
	if endIndex > len(sandboxes) {
		endIndex = len(sandboxes)
	}

	result.Sandboxes = sandboxes[startIndex:endIndex]

	if endIndex < len(sandboxes) {
		lastSandbox := result.Sandboxes[len(result.Sandboxes)-1]
		cursor := generateCursor(lastSandbox)
		result.NextToken = &cursor
	}

	return result, nil
}

func (a *APIStore) GetSandboxes(c *gin.Context, params api.GetSandboxesParams) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list running instances")

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed running instances", properties)

	sandboxes, err := a.getSandboxes(ctx, team.ID, SandboxesListParams{
		State: params.State,
		Query: params.Query,
	}, params.Limit)
	if err != nil {
		zap.L().Error("Error fetching sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning sandboxes for team '%s': %s", team.ID, err))

		return
	}

	// Paginate sandboxes
	result, err := paginateSandboxes(sandboxes, SandboxesListPaginate{
		NextToken: params.NextToken,
		Limit:     params.Limit,
	})
	if err != nil {
		zap.L().Error("Error fetching sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error paginating sandboxes for team '%s'", team.ID))

		return
	}

	sandboxes = result.Sandboxes

	// add pagination info to headers
	if result.NextToken != nil {
		c.Header("X-Next-Token", *result.NextToken)
	}
	c.Header("X-Total-Items", strconv.Itoa(len(sandboxes)))

	c.JSON(http.StatusOK, sandboxes)
}
