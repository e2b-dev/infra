package clusters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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

func (r *ClusterResourceProviderImpl) GetSandboxMetrics(ctx context.Context, teamID string, sandboxID string, qStart *int64, qEnd *int64) ([]api.SandboxMetric, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get-sandbox-metrics", trace.WithAttributes(attribute.String("provider", "remote")))
	defer span.End()

	req := &edgeapi.V1SandboxMetricsParams{
		TeamID: teamID,
		Start:  qStart,
		End:    qEnd,
	}

	res, err := r.client.V1SandboxMetricsWithResponse(ctx, sandboxID, req)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when getting sandbox metrics from edge API", err)

		return nil, &api.APIError{
			Err:       err,
			ClientMsg: "Error when getting sandbox metrics",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.StatusCode() != http.StatusOK {
		return nil, &api.APIError{
			Err:       fmt.Errorf("unexpected response with HTTP status '%d'", res.StatusCode()),
			ClientMsg: "Error when getting sandbox metrics",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.JSON200 == nil {
		return nil, &api.APIError{
			Err:       errors.New("request returned nil response"),
			ClientMsg: "Error when getting sandbox metrics",
			Code:      http.StatusInternalServerError,
		}
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

func (r *ClusterResourceProviderImpl) GetSandboxesMetrics(ctx context.Context, teamID string, sandboxIDs []string) (map[string]api.SandboxMetric, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get-sandboxes-metrics", trace.WithAttributes(attribute.String("provider", "remote")))
	defer span.End()

	res, err := r.client.V1SandboxesMetricsWithResponse(ctx, &edgeapi.V1SandboxesMetricsParams{TeamID: teamID, SandboxIds: sandboxIDs})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when getting sandboxes metrics from edge API", err)

		return nil, &api.APIError{
			Err:       err,
			ClientMsg: "Error when getting sandboxes metrics",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.StatusCode() != http.StatusOK {
		return nil, &api.APIError{
			Err:       fmt.Errorf("unexpected response with HTTP status '%d'", res.StatusCode()),
			ClientMsg: "Error when getting sandboxes metrics",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.JSON200 == nil {
		return nil, &api.APIError{
			Err:       errors.New("request returned nil response"),
			ClientMsg: "Error when getting sandboxes metrics",
			Code:      http.StatusInternalServerError,
		}
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

func (r *ClusterResourceProviderImpl) GetSandboxLogs(ctx context.Context, teamID string, sandboxID string, start *int64, limit *int32) (api.SandboxLogs, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get-sandbox-logs", trace.WithAttributes(attribute.String("provider", "remote")))
	defer span.End()

	res, err := r.client.V1SandboxLogsWithResponse(ctx, sandboxID, &edgeapi.V1SandboxLogsParams{TeamID: teamID, Start: start, Limit: limit})
	if err != nil {
		telemetry.ReportError(ctx, "error when fetching sandbox logs", err)

		return api.SandboxLogs{}, &api.APIError{
			Err:       fmt.Errorf("error when fetching sandbox logs: %w", err),
			ClientMsg: "Error when getting sandbox logs",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.StatusCode() != http.StatusOK {
		err = fmt.Errorf("unexpected response with HTTP status: %d", res.StatusCode())
		telemetry.ReportError(ctx, "error when fetching sandbox logs", err)

		return api.SandboxLogs{}, &api.APIError{
			Err:       err,
			ClientMsg: "Error when getting sandbox logs",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.JSON200 == nil {
		err = errors.New("sandbox logs request returned nil")
		telemetry.ReportError(ctx, err.Error(), err)

		return api.SandboxLogs{}, &api.APIError{
			Err:       err,
			ClientMsg: "Error when getting sandbox logs",
			Code:      http.StatusInternalServerError,
		}
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
) ([]logs.LogEntry, *api.APIError) {
	ctx, span := tracer.Start(ctx, "get-build-logs", trace.WithAttributes(attribute.String("provider", "remote")))
	defer span.End()

	// Use shared implementation with Edge API as the persistent log backend
	start, end := logQueryWindow(cursor, direction)
	persistentFetcher := r.getBuildLogsFromEdge(ctx, templateID, buildID, offset, limit, level, start, end, direction)

	entries, err := getBuildLogsWithSources(ctx, r.instances, nodeID, templateID, buildID, offset, limit, level, cursor, direction, source, persistentFetcher)
	if err != nil {
		telemetry.ReportError(ctx, "error when getting build logs", err, telemetry.WithTemplateID(templateID), telemetry.WithBuildID(buildID))

		return nil, &api.APIError{
			Err:       fmt.Errorf("error when fetching build logs: %w", err),
			ClientMsg: "Error when getting build logs",
			Code:      http.StatusInternalServerError,
		}
	}

	return entries, nil
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
