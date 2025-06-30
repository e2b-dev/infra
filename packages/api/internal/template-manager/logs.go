package template_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	loki "github.com/grafana/loki/pkg/logcli/client"
	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	lokiTemplateBuildLogsLimit       = 1_000
	lokiTemplateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)

type PlacementLogsProvider interface {
	GetLogs(ctx context.Context, buildId string, templateId string, offset *int32) ([]string, error)
}

type LokiPlacementLogsProvider struct {
	lokiClient *loki.DefaultClient
}

type ClusterPlacementLogsProvider struct {
	edgeHttpClient *api.ClientWithResponses
	nodeID         string
}

func NewClusterPlacementLogsProvider(edgeHttpClient *api.ClientWithResponses, nodeID string) PlacementLogsProvider {
	return &ClusterPlacementLogsProvider{edgeHttpClient: edgeHttpClient, nodeID: nodeID}
}

func (l *ClusterPlacementLogsProvider) GetLogs(ctx context.Context, buildID string, templateID string, offset *int32) ([]string, error) {
	res, err := l.edgeHttpClient.V1TemplateBuildLogsWithResponse(
		ctx, buildID, &api.V1TemplateBuildLogsParams{TemplateID: templateID, OrchestratorID: l.nodeID, Offset: offset},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get build logs in template manager: %w", err)
	}

	if res.StatusCode() != 200 {
		zap.L().Error("failed to get build logs in template manager", zap.String("body", string(res.Body)))
		return nil, errors.New("failed to get build logs in template manager")
	}

	return res.JSON200.Logs, nil
}

func NewLokiPlacementLogsProvider(lokiClient *loki.DefaultClient) PlacementLogsProvider {
	return &LokiPlacementLogsProvider{lokiClient: lokiClient}
}

func (l *LokiPlacementLogsProvider) GetLogs(ctx context.Context, buildID string, templateID string, offset *int32) ([]string, error) {
	if l.lokiClient == nil {
		return nil, fmt.Errorf("loki edgeHttpClient is not configured")
	}

	// Sanitize env ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIdSanitized := strings.ReplaceAll(templateID, "`", "")
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildID, templateIdSanitized)

	end := time.Now()
	start := end.Add(-lokiTemplateBuildOldestLogsLimit)
	logs := make([]string, 0)

	res, err := l.lokiClient.QueryRange(query, lokiTemplateBuildLogsLimit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err == nil {
		logsCrawled := 0
		logsOffset := 0
		if offset != nil {
			logsOffset = int(*offset)
		}

		if res.Data.Result.Type() != loghttp.ResultTypeStream {
			zap.L().Error("unexpected value type received from loki query fetch", zap.String("type", string(res.Data.Result.Type())))
			return nil, fmt.Errorf("unexpected value type received from loki query fetch")
		}

		for _, stream := range res.Data.Result.(loghttp.Streams) {
			for _, entry := range stream.Entries {
				logsCrawled++

				// loki does not support offset pagination, so we need to skip logs manually
				if logsCrawled <= logsOffset {
					continue
				}

				line := make(map[string]interface{})
				err := json.Unmarshal([]byte(entry.Line), &line)
				if err != nil {
					zap.L().Error("error parsing log line", zap.Error(err), logger.WithBuildID(buildID), zap.String("line", entry.Line))
				}

				logs = append(logs, line["message"].(string))
			}
		}
	} else {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))
	}

	return logs, nil
}
