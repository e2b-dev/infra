package logger_provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	loki "github.com/grafana/loki/pkg/logcli/client"
	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var (
	lokiAddressEnvName = "LOKI_URL"
	lokiAddress        = os.Getenv(lokiAddressEnvName)
)

type LokiQueryProvider struct {
	client *loki.DefaultClient
}

func NewLokiQueryProvider() (*LokiQueryProvider, error) {
	var lokiClient *loki.DefaultClient

	if lokiAddress == "" {
		return nil, fmt.Errorf("loki address is empty, please set the %s environment variable", lokiAddressEnvName)
	}

	// optional authentication supported
	lokiUser := os.Getenv("LOKI_USER")
	lokiPassword := os.Getenv("LOKI_PASSWORD")

	lokiClient = &loki.DefaultClient{
		Address:  lokiAddress,
		Username: lokiUser,
		Password: lokiPassword,
	}

	return &LokiQueryProvider{client: lokiClient}, nil
}

func (l *LokiQueryProvider) QueryBuildLogs(ctx context.Context, templateId string, buildId string, start time.Time, end time.Time, limit int, offset int) ([]LogEntry, error) {
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIdSanitized := strings.ReplaceAll(templateId, "`", "")
	buildIdSanitized := strings.ReplaceAll(buildId, "`", "")

	// todo: service name is different here (because new merged orchestrator)
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildIdSanitized, templateIdSanitized)

	res, err := l.client.QueryRange(query, limit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		errMsg := fmt.Errorf("error when returning logs for template build: %w", err)
		telemetry.ReportError(ctx, errMsg)
		zap.L().Error("error when returning logs for template build", zap.Error(err), zap.String("buildId", buildId))
		return make([]LogEntry, 0), nil
	}

	logs, err := l.lokiResponseMapper(res, offset)
	if err != nil {
		telemetry.ReportError(ctx, fmt.Errorf("error when mapping build logs: %w", err))
		zap.L().Error("error when mapping logs for template build", zap.Error(err), zap.String("buildId", buildId))
		return make([]LogEntry, 0), nil
	}

	return logs, nil
}

func (l *LokiQueryProvider) QuerySandboxLogs(ctx context.Context, teamId string, sandboxId string, start time.Time, end time.Time, limit int, offset int) ([]LogEntry, error) {
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	sandboxIdSanitized := strings.ReplaceAll(sandboxId, "`", "")
	teamIdSanitized := strings.ReplaceAll(teamId, "`", "")

	query := fmt.Sprintf("{teamID=`%s`, sandboxID=`%s`, category!=\"metrics\"}", teamIdSanitized, sandboxIdSanitized)

	res, err := l.client.QueryRange(query, limit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		errMsg := fmt.Errorf("error when returning logs for sandbox: %w", err)
		telemetry.ReportError(ctx, errMsg)
		zap.L().Error("error when returning logs for sandbox", zap.Error(err), zap.String("sandboxId", sandboxId))
		return make([]LogEntry, 0), nil
	}

	logs, err := l.lokiResponseMapper(res, offset)
	if err != nil {
		telemetry.ReportError(ctx, fmt.Errorf("error when mapping sandbox logs: %w", err))
		zap.L().Error("error when mapping logs for sandbox", zap.Error(err), zap.String("sandboxId", sandboxId))
		return make([]LogEntry, 0), nil
	}

	return logs, nil
}

func (l *LokiQueryProvider) lokiResponseMapper(res *loghttp.QueryResponse, offset int) ([]LogEntry, error) {
	logsCrawled := 0
	logs := make([]LogEntry, 0)

	if res.Data.Result.Type() != loghttp.ResultTypeStream {
		return nil, fmt.Errorf("unexpected value type received from loki query fetch: %s", res.Data.Result.Type())
	}

	for _, stream := range res.Data.Result.(loghttp.Streams) {
		for _, entry := range stream.Entries {
			logsCrawled++

			// loki does not support offset pagination, so we need to skip logs manually
			if logsCrawled <= offset {
				continue
			}

			line := make(map[string]interface{})
			err := json.Unmarshal([]byte(entry.Line), &line)
			if err != nil {
				zap.L().Error("error parsing log line", zap.Error(err), zap.String("line", entry.Line))
			}

			logs = append(logs, LogEntry{Line: line["message"].(string), Timestamp: entry.Timestamp})
		}
	}

	// Sort logs by timestamp (they are returned by the time they arrived in Loki)
	slices.SortFunc(logs, func(a, b LogEntry) int { return a.Timestamp.Compare(b.Timestamp) })

	return logs, nil
}
