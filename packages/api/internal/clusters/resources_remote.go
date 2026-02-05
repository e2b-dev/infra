package clusters

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
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

func (r *ClusterResourceProviderImpl) GetSandboxMetrics(ctx context.Context, teamID string, sandboxID string, qStart *int64, qEnd *int64) ([]api.SandboxMetric, *api.APIError) {
	req := &edgeapi.V1SandboxMetricsParams{
		TeamID: teamID,
		Start:  qStart,
		End:    qEnd,
	}

	res, err := r.client.V1SandboxMetricsWithResponse(ctx, sandboxID, req)
	if err != nil {
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: "Failed to fetch sandbox metrics",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return nil, handleEdgeErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON500, "Failed to fetch sandbox metrics")
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
	res, err := r.client.V1SandboxesMetricsWithResponse(ctx, &edgeapi.V1SandboxesMetricsParams{TeamID: teamID, SandboxIds: sandboxIDs})
	if err != nil {
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: "Failed to fetch sandboxes metrics",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return nil, handleEdgeErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON500, "Failed to fetch sandboxes metrics")
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

func (r *ClusterResourceProviderImpl) GetSandboxLogs(ctx context.Context, teamID string, sandboxID string, start *int64, end *int64, limit *int32, dr *api.LogsDirection) (api.SandboxLogs, *api.APIError) {
	var direction *edgeapi.V1SandboxLogsParamsDirection
	if dr != nil {
		if *dr == api.LogsDirectionBackward {
			direction = utils.ToPtr(edgeapi.V1SandboxLogsParamsDirectionBackward)
		} else {
			direction = utils.ToPtr(edgeapi.V1SandboxLogsParamsDirectionForward)
		}
	}

	params := &edgeapi.V1SandboxLogsParams{TeamID: teamID, Start: start, End: end, Limit: limit, Direction: direction}
	res, err := r.client.V1SandboxLogsWithResponse(ctx, sandboxID, params)
	if err != nil {
		return api.SandboxLogs{}, &api.APIError{
			Err:       err,
			ClientMsg: "Failed to fetch sandbox logs",
			Code:      http.StatusInternalServerError,
		}
	}

	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return api.SandboxLogs{}, handleEdgeErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON500, "Failed to fetch sandbox logs")
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
	// Use shared implementation with Edge API as the persistent log backend
	start, end := logQueryWindow(cursor, direction)
	persistentFetcher := r.getBuildLogsFromEdge(ctx, templateID, buildID, offset, limit, level, start, end, direction)

	return getBuildLogsWithSources(ctx, r.instances, nodeID, templateID, buildID, offset, limit, level, cursor, direction, source, persistentFetcher)
}

func (r *ClusterResourceProviderImpl) getBuildLogsFromEdge(ctx context.Context, templateID string, buildID string, offset int32, limit int32, level *logs.LogLevel, start time.Time, end time.Time, direction api.LogsDirection) logSourceFunc {
	return func() ([]logs.LogEntry, *api.APIError) {
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
			return nil, &api.APIError{
				Err:       fmt.Errorf("failed to get build logs in template manager: %w", err),
				ClientMsg: "Failed to fetch build logs",
				Code:      http.StatusInternalServerError,
			}
		}

		if res.StatusCode() != 200 || res.JSON200 == nil {
			return nil, handleEdgeErrorResponse(res.StatusCode(), res.JSON400, res.JSON401, res.JSON500, "Failed to fetch build logs")
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

// handleEdgeErrorResponse extracts error information from edge API error responses
func handleEdgeErrorResponse(statusCode int, json400, json401, json500 *edgeapi.Error, clientMsg string) *api.APIError {
	// Try to extract error message from response body
	var errMsg string
	switch statusCode {
	case http.StatusBadRequest:
		if json400 != nil && json400.Message != "" {
			return &api.APIError{
				Err:       fmt.Errorf("bad request: %s", json400.Message),
				ClientMsg: json400.Message,
				Code:      http.StatusBadRequest,
			}
		}
	case http.StatusUnauthorized:
		if json401 != nil && json401.Message != "" {
			errMsg = json401.Message
		}
	case http.StatusInternalServerError:
		if json500 != nil && json500.Message != "" {
			errMsg = json500.Message
		}
	}

	if errMsg == "" {
		errMsg = "Unexpected error occurred"
	}

	return &api.APIError{
		Err:       fmt.Errorf("edge error: %s (http code %d)", errMsg, statusCode),
		ClientMsg: clientMsg,
		Code:      http.StatusInternalServerError,
	}
}
