package handlers

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/snapshot"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxes(c *gin.Context, params api.GetSandboxesParams) {
	ctx := c.Request.Context()

	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)
	team := teamInfo.Team

	telemetry.ReportEvent(ctx, "list running instances")

	instanceInfo := a.orchestrator.GetSandboxes(ctx, &team.ID)

	// all sandbox ids of current running sandboxes
	instanceSandboxIDs := make([]string, 0)
	for _, info := range instanceInfo {
		instanceSandboxIDs = append(instanceSandboxIDs, info.Instance.SandboxID)
	}

	// all snapshots where env team is same as team.ID and sandbox_id is not included in instanceInfo.SandboxID
	snapshots, err := a.db.Client.Snapshot.Query().
		Where(snapshot.HasEnvWith(env.TeamID(team.ID))).
		Where(snapshot.SandboxIDNotIn(instanceSandboxIDs...)).
		WithEnv(func(query *models.EnvQuery) {
			query.
				WithBuilds(func(query *models.EnvBuildQuery) {
					query.Where(envbuild.StatusEQ(envbuild.StatusUploaded)).
						Order(models.Desc(envbuild.FieldFinishedAt))
				}).
				WithEnvAliases()
		}).
		All(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	latestSnapshots := make(map[string]*models.Snapshot)
	for _, s := range snapshots {
		if existing, ok := latestSnapshots[s.SandboxID]; !ok || s.CreatedAt.After(existing.CreatedAt) {
			latestSnapshots[s.SandboxID] = s
		}
	}

	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "listed running instances", properties)

	buildIDs := make([]uuid.UUID, 0)
	for _, info := range instanceInfo {
		if info.TeamID == nil {
			continue
		}

		if *info.TeamID != team.ID {
			continue
		}

		buildIDs = append(buildIDs, *info.BuildID)
	}

	builds, err := a.db.Client.EnvBuild.Query().Where(envbuild.IDIn(buildIDs...)).All(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, err)

		return
	}

	buildsMap := make(map[uuid.UUID]*models.EnvBuild, len(builds))
	for _, build := range builds {
		buildsMap[build.ID] = build
	}

	sandboxes := make([]api.RunningSandbox, 0)

	for _, info := range instanceInfo {
		if info.TeamID == nil {
			continue
		}

		if *info.TeamID != team.ID {
			continue
		}

		if info.BuildID == nil {
			continue
		}

		instance := api.RunningSandbox{
			ClientID:   info.Instance.ClientID,
			TemplateID: info.Instance.TemplateID,
			Alias:      info.Instance.Alias,
			SandboxID:  info.Instance.SandboxID,
			StartedAt:  info.StartTime,
			CpuCount:   int32(buildsMap[*info.BuildID].Vcpu),
			MemoryMB:   int32(buildsMap[*info.BuildID].RAMMB),
			EndAt:      info.EndTime,
			State:      "running",
		}

		if info.Metadata != nil {
			meta := api.SandboxMetadata(info.Metadata)
			instance.Metadata = &meta
		}

		sandboxes = append(sandboxes, instance)
	}

	// append latest snapshots to sandboxes
	for _, s := range latestSnapshots {
		build := s.Edges.Env.Edges.Builds[0]
		alias := s.Edges.Env.Edges.EnvAliases[0]

		instance := api.RunningSandbox{
			ClientID:   "",
			TemplateID: s.EnvID,
			Alias:      &alias.ID,
			SandboxID:  s.SandboxID,
			StartedAt:  s.SandboxStartedAt,
			CpuCount:   int32(build.Vcpu),
			MemoryMB:   int32(build.RAMMB),
			EndAt:      s.PausedAt,
			State:      "paused",
		}

		if s.Metadata != nil {
			meta := api.SandboxMetadata(s.Metadata)
			instance.Metadata = &meta
		}

		sandboxes = append(sandboxes, instance)
	}

	// filter sandboxes by metadata
	if params.Query != nil {
		// Unescape query
		query, err := url.QueryUnescape(*params.Query)
		if err != nil {
			c.JSON(http.StatusBadRequest, "Error when unescaping query")

			return
		}

		// Parse filters, both key and value are also unescaped
		filters := make(map[string]string)

		for _, filter := range strings.Split(query, "&") {
			parts := strings.Split(filter, "=")
			if len(parts) != 2 {
				c.JSON(http.StatusBadRequest, "Invalid key value pair in query")

				return
			}

			key, err := url.QueryUnescape(parts[0])
			if err != nil {
				c.JSON(http.StatusBadRequest, "Error when unescaping key")

				return
			}

			value, err := url.QueryUnescape(parts[1])
			if err != nil {
				c.JSON(http.StatusBadRequest, "Error when unescaping value")

				return
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

	// filter sandboxes by state
	if params.State != nil {
		if *params.State == "running" {
			filtered := make([]api.RunningSandbox, 0, len(sandboxes))
			for _, s := range sandboxes {
				if s.State == "running" {
					filtered = append(filtered, s)
				}
			}
			sandboxes = filtered
		} else if *params.State == "paused" {
			filtered := make([]api.RunningSandbox, 0, len(sandboxes))
			for _, s := range sandboxes {
				if s.State == "paused" {
					filtered = append(filtered, s)
				}
			}
			sandboxes = filtered
		}
	}

	// Sort sandboxes by start time descending
	slices.SortFunc(sandboxes, func(a, b api.RunningSandbox) int {
		return a.StartedAt.Compare(b.StartedAt)
	})

	c.JSON(http.StatusOK, sandboxes)
}
