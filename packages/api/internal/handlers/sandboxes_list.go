package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type SandboxesListParams struct {
	State *[]api.SandboxState
}

type SandboxesListFilter struct {
	Query *string
}

func (a *APIStore) getSandboxes(ctx context.Context, teamID uuid.UUID, params SandboxesListParams) ([]api.ListedSandbox, error) {
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
		// Get build IDs for running sandboxes
		buildIDs := make([]uuid.UUID, 0)
		for _, info := range runningSandboxes {
			if info.BuildID != nil {
				buildIDs = append(buildIDs, *info.BuildID)
			}
		}

		// Fetch builds for running sandboxes
		builds, err := a.db.Client.EnvBuild.Query().Where(envbuild.IDIn(buildIDs...)).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting builds for running sandboxes: %s", err)
		}

		buildsMap := make(map[uuid.UUID]*models.EnvBuild, len(builds))
		for _, build := range builds {
			buildsMap[build.ID] = build
		}

		// Add running sandboxes to results
		for _, info := range runningSandboxes {
			if info.BuildID == nil {
				continue
			}

			memoryMB := int32(-1)
			cpuCount := int32(-1)

			if buildsMap[*info.BuildID] != nil {
				memoryMB = int32(buildsMap[*info.BuildID].RAMMB)
				cpuCount = int32(buildsMap[*info.BuildID].Vcpu)
			}

			sandbox := api.ListedSandbox{
				ClientID:   info.Instance.ClientID,
				TemplateID: info.Instance.TemplateID,
				Alias:      info.Instance.Alias,
				SandboxID:  info.Instance.SandboxID,
				StartedAt:  info.StartTime,
				CpuCount:   cpuCount,
				MemoryMB:   memoryMB,
				EndAt:      info.EndTime,
				State:      api.Running,
			}

			if info.Metadata != nil {
				meta := api.SandboxMetadata(info.Metadata)
				sandbox.Metadata = &meta
			}

			sandboxes = append(sandboxes, sandbox)
		}
	}

	// Only fetch snapshots if we need them (state is nil or "paused")
	if params.State == nil || slices.Contains(*params.State, api.Paused) {
		snapshotEnvs, err := a.db.GetTeamSnapshots(ctx, teamID, runningSandboxesIDs)
		if err != nil {
			return nil, fmt.Errorf("error getting team snapshots: %s", err)
		}

		// Add snapshots to results
		for _, e := range snapshotEnvs {
			snapshotBuilds := e.Edges.Builds
			snapshot := e.Edges.Snapshots[0]

			memoryMB := int32(-1)
			cpuCount := int32(-1)

			if len(snapshotBuilds) > 0 {
				memoryMB = int32(snapshotBuilds[0].RAMMB)
				cpuCount = int32(snapshotBuilds[0].Vcpu)
			}

			sandbox := api.ListedSandbox{
				ClientID:   "00000000", // for backwards compatibility we need to return a client id
				TemplateID: e.ID,
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
	}

	return sandboxes, nil
}

func filterSandboxes(sandboxes []api.ListedSandbox, filter SandboxesListFilter) ([]api.ListedSandbox, error) {
	// filter sandboxes by metadata
	if filter.Query != nil {
		// Unescape query
		query, err := url.QueryUnescape(*filter.Query)
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
	}

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

	sandboxes, err := a.getSandboxes(ctx, team.ID, SandboxesListParams{
		State: params.State,
	})
	if err != nil {
		a.logger.Error("Error fetching sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning sandboxes for team '%s': %s", team.ID, err))

		return
	}

	sandboxes, err = filterSandboxes(sandboxes, SandboxesListFilter{
		Query: params.Query,
	})
	if err != nil {
		a.logger.Error("Error filtering sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error filtering sandboxes for team '%s': %s", team.ID, err))

		return
	}

	// Sort by startedAt descending
	slices.SortFunc(sandboxes, func(a, b api.ListedSandbox) int {
		return b.StartedAt.Compare(a.StartedAt)
	})

	c.JSON(http.StatusOK, sandboxes)
}
