package clusters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type ClusterResourceProviderImpl struct {
	instances *smap.Map[*Instance]
	client    *edgeapi.ClientWithResponses
}

func newRemoteClusterResourceProvider(instances *smap.Map[*Instance], client *edgeapi.ClientWithResponses) ClusterResource {
	return &ClusterResourceProviderImpl{
		instances: instances,
		client:    client,
	}
}

func (r *ClusterResourceProviderImpl) GetSandboxMetrics(ctx context.Context, teamID string, sandboxID string, qStart *int64, qEnd *int64) ([]api.SandboxMetric, error) {
	req := &edgeapi.V1SandboxMetricsParams{
		TeamID: teamID,
		Start:  qStart,
		End:    qEnd,
	}

	res, err := r.client.V1SandboxMetricsWithResponse(ctx, sandboxID, req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("unexpected response with HTTP status '%d'", res.StatusCode())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request returned nil response")
	}

	raw := *res.JSON200
	items := make([]api.SandboxMetric, len(raw))
	for i, m := range raw {
		items[i] = api.SandboxMetric{
			Timestamp:     m.Timestamp,
			TimestampUnix: m.TimestampUnix,
			CpuUsedPct:    m.CpuUsedPct,
			CpuCount:      m.CpuCount,
			MemTotal:      m.MemTotal,
			MemUsed:       m.MemUsed,
			DiskTotal:     m.DiskTotal,
			DiskUsed:      m.DiskUsed,
		}
	}

	return items, nil
}

func (r *ClusterResourceProviderImpl) GetSandboxesMetrics(ctx context.Context, teamID string, sandboxIDs []string) (map[string]api.SandboxMetric, error) {
	res, err := r.client.V1SandboxesMetricsWithResponse(ctx, &edgeapi.V1SandboxesMetricsParams{TeamID: teamID, SandboxIds: sandboxIDs})
	if err != nil {
		return nil, err
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("unexpected response with HTTP status '%d'", res.StatusCode())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request returned nil response")
	}

	raw := *res.JSON200
	items := make(map[string]api.SandboxMetric, len(raw.Sandboxes))
	for sbxID, v := range raw.Sandboxes {
		items[sbxID] = api.SandboxMetric{
			Timestamp:     v.Timestamp,
			TimestampUnix: v.TimestampUnix,
			CpuUsedPct:    v.CpuUsedPct,
			CpuCount:      v.CpuCount,
			MemTotal:      v.MemTotal,
			MemUsed:       v.MemUsed,
			DiskTotal:     v.DiskTotal,
			DiskUsed:      v.DiskUsed,
		}
	}

	return items, nil
}

func (r *ClusterResourceProviderImpl) GetSandboxLogs(ctx context.Context, teamID string, sandboxID string, start *int64, limit *int32) (api.SandboxLogs, error) {
	res, err := r.client.V1SandboxLogsWithResponse(ctx, sandboxID, &edgeapi.V1SandboxLogsParams{TeamID: teamID, Start: start, Limit: limit})
	if err != nil {
		return api.SandboxLogs{}, err
	}

	if res.StatusCode() != http.StatusOK {
		return api.SandboxLogs{}, fmt.Errorf("unexpected response with HTTP status '%d'", res.StatusCode())
	}

	if res.JSON200 == nil {
		return api.SandboxLogs{}, errors.New("request returned nil response")
	}

	raw := *res.JSON200
	l := make([]api.SandboxLog, len(raw.Logs))
	for i, row := range raw.Logs {
		l[i] = api.SandboxLog{
			Line:      row.Line,
			Timestamp: row.Timestamp,
		}
	}

	le := make([]api.SandboxLogEntry, len(raw.LogEntries))
	for i, row := range raw.LogEntries {
		le[i] = api.SandboxLogEntry{
			Timestamp: row.Timestamp,
			Level:     api.LogLevel(row.Level),
			Message:   row.Message,
			Fields:    row.Fields,
		}
	}

	return api.SandboxLogs{Logs: l, LogEntries: le}, nil
}

func (r *ClusterResourceProviderImpl) GetBuildLogs(
	ctx context.Context,
	nodeID *string,
	templateID string,
	buildID string,
	offset int32,
	limit int32,
	level *logs.LogLevel,
	cursor *time.Time,
	direction api.LogsDirection,
	source *api.LogsSource,
) ([]logs.LogEntry, error) {
	ctx, span := tracer.Start(ctx, "get build-logs")
	defer span.End()

	start, end := logQueryWindow(cursor, direction)

	var sources []logSourceFunc

	if nodeID != nil && logCheckSourceType(source, api.LogsSourceTemporary) {
		instance, found := r.instances.Get(*nodeID)
		if !found {
			return nil, fmt.Errorf("node instance not found for id '%s'", *nodeID)
		}

		sourceCallback := logsFromBuilderInstance(ctx, instance, templateID, buildID, offset, limit, level, start, end, direction)
		sources = append(sources, sourceCallback)
	}

	if logCheckSourceType(source, api.LogsSourcePersistent) {
		sourceCallback := r.getBuildLogsFromEdge(ctx, templateID, buildID, offset, limit, level, start, end, direction)
		sources = append(sources, sourceCallback)
	}

	// Iterate through sources and return the first successful fetch,
	// it depends on nodeID and source what sources are available here.
	for _, sourceFetch := range sources {
		entries, err := sourceFetch()
		if err != nil {
			logger.L().Warn(ctx, "Error fetching build logs", logger.WithTemplateID(templateID), logger.WithBuildID(buildID), zap.Error(err))

			continue
		}

		return entries, nil
	}

	return nil, nil
}

func (r *ClusterResourceProviderImpl) getBuildLogsFromEdge(ctx context.Context, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, start time.Time, end time.Time, direction api.LogsDirection) logSourceFunc {
	return func() ([]logs.LogEntry, error) {
		res, err := r.client.V1TemplateBuildLogsWithResponse(
			ctx, buildID, &edgeapi.V1TemplateBuildLogsParams{
				TemplateID: templateID,
				Offset:     &offset,
				Limit:      &limit,
				Level:      logToEdgeLevel(level),
				// TODO: remove this once the API spec is not required to have orchestratorID (https://linear.app/e2b/issue/ENG-3352)
				OrchestratorID: utils.ToPtr("unused"),
				Start:          utils.ToPtr(start.UnixMilli()),
				End:            utils.ToPtr(end.UnixMilli()),
				Direction:      utils.ToPtr(edgeapi.V1TemplateBuildLogsParamsDirection(direction)),
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to get build logs in template manager: %w", err)
		}

		if res.StatusCode() != 200 {
			return nil, errors.New("failed to get build logs in template manager")
		}

		if res.JSON200 == nil {
			return nil, errors.New("request returned nil response")
		}

		raw := *res.JSON200
		l := make([]logs.LogEntry, len(raw.LogEntries))
		for i, entry := range raw.LogEntries {
			l[i] = logs.LogEntry{
				Timestamp: entry.Timestamp,
				Message:   entry.Message,
				Level:     logs.StringToLevel(string(entry.Level)),
				Fields:    entry.Fields,
			}
		}

		return l, nil
	}
}
