package logger_provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	loki "github.com/grafana/loki/pkg/logcli/client"
	"github.com/grafana/loki/pkg/logproto"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
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

func (l *LokiQueryProvider) QueryBuildLogs(ctx context.Context, templateID string, buildID string, start time.Time, end time.Time, limit int, offset int32, level *logs.LogLevel) ([]logs.LogEntry, error) {
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIDSanitized := strings.ReplaceAll(templateID, "`", "")
	buildIDSanitized := strings.ReplaceAll(buildID, "`", "")

	// todo: service name is different here (because new merged orchestrator)
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildIDSanitized, templateIDSanitized)

	res, err := l.client.QueryRange(query, limit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))
		return make([]logs.LogEntry, 0), nil
	}

	lm, err := logs.LokiResponseMapper(res, offset, level)
	if err != nil {
		telemetry.ReportError(ctx, "error when mapping build logs", err)
		zap.L().Error("error when mapping logs for template build", zap.Error(err), logger.WithBuildID(buildID))
		return make([]logs.LogEntry, 0), nil
	}

	return lm, nil
}

func (l *LokiQueryProvider) QuerySandboxLogs(ctx context.Context, teamID string, sandboxID string, start time.Time, end time.Time, limit int) ([]logs.LogEntry, error) {
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	sandboxIdSanitized := strings.ReplaceAll(sandboxID, "`", "")
	teamIdSanitized := strings.ReplaceAll(teamID, "`", "")

	query := fmt.Sprintf("{teamID=`%s`, sandboxID=`%s`, category!=\"metrics\"}", teamIdSanitized, sandboxIdSanitized)

	res, err := l.client.QueryRange(query, limit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for sandbox", err)
		zap.L().Error("error when returning logs for sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))
		return make([]logs.LogEntry, 0), nil
	}

	lm, err := logs.LokiResponseMapper(res, 0, nil)
	if err != nil {
		telemetry.ReportError(ctx, "error when mapping sandbox logs", err)
		zap.L().Error("error when mapping logs for sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))
		return make([]logs.LogEntry, 0), nil
	}

	return lm, nil
}
