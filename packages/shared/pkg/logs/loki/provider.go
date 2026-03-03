package loki

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	loki "github.com/grafana/loki/v3/pkg/logcli/client"
	"github.com/grafana/loki/v3/pkg/logproto"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type LokiQueryProvider struct {
	client *loki.DefaultClient
}

const (
	DefaultDirection = logproto.FORWARD
)

func NewLokiQueryProvider(lokiURL string, lokiUser string, lokiPassword string) (*LokiQueryProvider, error) {
	lokiClient := &loki.DefaultClient{
		Address:  lokiURL,
		Username: lokiUser,
		Password: lokiPassword,
	}

	return &LokiQueryProvider{client: lokiClient}, nil
}

func (l *LokiQueryProvider) QueryBuildLogs(
	ctx context.Context,
	templateID string,
	buildID string,
	start time.Time,
	end time.Time,
	limit int,
	offset int32,
	level *logs.LogLevel,
	direction logproto.Direction,
) ([]logs.LogEntry, error) {
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIDSanitized := sanitizeLokiLabel(templateID)
	buildIDSanitized := sanitizeLokiLabel(buildID)

	query := buildBuildLogsQuery(templateIDSanitized, buildIDSanitized, level)

	res, err := l.client.QueryRange(query, limit, start, end, direction, time.Duration(0), time.Duration(0), true)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		logger.L().Error(ctx, "error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildID))

		return make([]logs.LogEntry, 0), nil
	}

	lm, err := ResponseMapper(ctx, res, offset, direction)
	if err != nil {
		telemetry.ReportError(ctx, "error when mapping build logs", err)
		logger.L().Error(ctx, "error when mapping logs for template build", zap.Error(err), logger.WithBuildID(buildID))

		return make([]logs.LogEntry, 0), nil
	}

	return lm, nil
}

func (l *LokiQueryProvider) QuerySandboxLogs(
	ctx context.Context,
	teamID string,
	sandboxID string,
	start time.Time,
	end time.Time,
	limit int,
	direction logproto.Direction,
	level *logs.LogLevel,
	search *string,
) ([]logs.LogEntry, error) {
	query := buildSandboxLogsQuery(teamID, sandboxID, level, search)

	res, err := l.client.QueryRange(query, limit, start, end, direction, time.Duration(0), time.Duration(0), true)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for sandbox", err)
		logger.L().Error(ctx, "error when returning logs for sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

		return make([]logs.LogEntry, 0), nil
	}

	lm, err := ResponseMapper(ctx, res, 0, direction)
	if err != nil {
		telemetry.ReportError(ctx, "error when mapping sandbox logs", err)
		logger.L().Error(ctx, "error when mapping logs for sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

		return make([]logs.LogEntry, 0), nil
	}

	return lm, nil
}

func sanitizeLokiLabel(input string) string {
	return strings.ReplaceAll(input, "`", "")
}

func sanitizeLogMessageRegexFilter(input string) string {
	return fmt.Sprintf(".*%s.*", regexp.QuoteMeta(strings.ReplaceAll(input, "`", "")))
}

func minLevelRegexFilter(level logs.LogLevel) string {
	switch level {
	case logs.LevelError:
		return "error"
	case logs.LevelWarn:
		return "(warn|error)"
	case logs.LevelInfo:
		return "(|info|warn|error)"
	default:
		return "(|debug|info|warn|error)"
	}
}

func buildBuildLogsQuery(templateID string, buildID string, level *logs.LogLevel) string {
	// todo: service name is different here (because new merged orchestrator)
	query := fmt.Sprintf("{service=\"template-manager\", buildID=`%s`, envID=`%s`}", buildID, templateID)
	if level == nil {
		return query
	}

	return query + fmt.Sprintf(" | json | level =~ `%s`", minLevelRegexFilter(*level))
}

func buildSandboxLogsQuery(teamID string, sandboxID string, level *logs.LogLevel, search *string) string {
	sandboxIDSanitized := sanitizeLokiLabel(sandboxID)
	teamIDSanitized := sanitizeLokiLabel(teamID)
	query := fmt.Sprintf("{teamID=`%s`, sandboxID=`%s`, category!=\"metrics\"}", teamIDSanitized, sandboxIDSanitized)
	if level == nil && (search == nil || *search == "") {
		return query
	}

	query += " | json"
	if level != nil {
		query += fmt.Sprintf(" | level =~ `%s`", minLevelRegexFilter(*level))
	}
	if search != nil && *search != "" {
		query += fmt.Sprintf(" | message =~ `%s`", sanitizeLogMessageRegexFilter(*search))
	}

	return query
}
