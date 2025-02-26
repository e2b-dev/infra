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
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) getSandboxes(ctx context.Context, teamID uuid.UUID, query *string) ([]api.RunningSandbox, error) {
	instanceInfo := a.orchestrator.GetSandboxes(ctx, &teamID)

	if query != nil {
		// Unescape query
		query, err := url.QueryUnescape(*query)
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
		for _, instance := range instanceInfo {
			if instance.Metadata == nil {
				continue
			}

			matchesAll := true
			for key, value := range filters {
				if metadataValue, ok := instance.Metadata[key]; !ok || metadataValue != value {
					matchesAll = false
					break
				}
			}

			if matchesAll {
				instanceInfo[n] = instance
				n++
			}
		}

		// Trim slice
		instanceInfo = instanceInfo[:n]
	}

	buildIDs := make([]uuid.UUID, 0)
	for _, info := range instanceInfo {
		if info.TeamID == nil {
			continue
		}

		if *info.TeamID != teamID {
			continue
		}

		buildIDs = append(buildIDs, *info.BuildID)
	}

	builds, err := a.db.Client.EnvBuild.Query().Where(envbuild.IDIn(buildIDs...)).All(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, err)

		return nil, fmt.Errorf("error when getting builds: %w", err)
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

		if *info.TeamID != teamID {
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
		}

		if info.Metadata != nil {
			meta := api.SandboxMetadata(info.Metadata)
			instance.Metadata = &meta
		}

		sandboxes = append(sandboxes, instance)
	}

	// Sort sandboxes by start time descending
	slices.SortFunc(sandboxes, func(a, b api.RunningSandbox) int {
		return a.StartedAt.Compare(b.StartedAt)
	})

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

	sandboxes, err := a.getSandboxes(ctx, team.ID, params.Query)
	if err != nil {
		zap.L().Error("Error fetching sandboxes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error returning sandboxes for team '%s'", team.ID))

		return
	}

	c.JSON(http.StatusOK, sandboxes)
}
