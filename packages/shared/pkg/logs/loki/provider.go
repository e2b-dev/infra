package loki

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	loki "github.com/grafana/loki/v3/pkg/logcli/client"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// ErrInvalidQuery indicates that the (user-supplied) LogQL query failed to
// parse. It is a client error (the caller passed a malformed query), as opposed
// to an upstream/transport failure talking to Loki. Callers can use errors.Is to
// map it to an HTTP 400 instead of a 500.
var ErrInvalidQuery = errors.New("invalid log query")

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
	query := buildBuildLogsQuery(templateID, buildID, level)

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
	pipeline *string,
) ([]logs.LogEntry, error) {
	query := buildSandboxLogsQuery(teamID, sandboxID, level, search, pipeline)

	// Validate the query locally before sending it to Loki. The stream selector and
	// the structured level/search filters are always built (and sanitized) server-side,
	// so the only way the final query is malformed is a bad client-supplied pipeline.
	// Catching it here returns a helpful parse error instead of the opaque, retried
	// failure the Loki client surfaces for a 400 response.
	if _, err := syntax.ParseExpr(query); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	}

	res, err := l.client.QueryRange(query, limit, start, end, direction, time.Duration(0), time.Duration(0), true)
	if err != nil {
		telemetry.ReportError(ctx, "error when returning logs for sandbox", err)
		logger.L().Error(ctx, "error when returning logs for sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

		return nil, fmt.Errorf("error when returning logs for sandbox: %w", err)
	}

	lm, err := ResponseMapper(ctx, res, 0, direction)
	if err != nil {
		telemetry.ReportError(ctx, "error when mapping sandbox logs", err)
		logger.L().Error(ctx, "error when mapping logs for sandbox", zap.Error(err), logger.WithSandboxID(sandboxID))

		return nil, fmt.Errorf("error when mapping sandbox logs: %w", err)
	}

	return lm, nil
}

// sanitizeLokiLabel removes backticks from label values to avoid breaking LogQL selectors.
// refs:
// - https://grafana.com/docs/loki/latest/query/log_queries/
// - https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
func sanitizeLokiLabel(input string) string {
	return strings.ReplaceAll(input, "`", "")
}

// sanitizeLogMessageRegexFilter quotes user input so search remains a literal substring match.
// refs:
// - https://grafana.com/docs/loki/latest/query/log_queries/
// - https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
func sanitizeLogMessageRegexFilter(input string) string {
	return fmt.Sprintf(".*%s.*", regexp.QuoteMeta(sanitizeLokiLabel(input)))
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
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIDSanitized := sanitizeLokiLabel(templateID)
	buildIDSanitized := sanitizeLokiLabel(buildID)

	// todo: service name is different here (because new merged orchestrator)
	query := fmt.Sprintf("{service=\"template-manager\", buildID=`%s`, envID=`%s`}", buildIDSanitized, templateIDSanitized)
	if level == nil {
		return query
	}

	return query + fmt.Sprintf(" | json | level =~ `%s`", minLevelRegexFilter(*level))
}

func buildSandboxLogsQuery(teamID string, sandboxID string, level *logs.LogLevel, search *string, pipeline *string) string {
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	sandboxIDSanitized := sanitizeLokiLabel(sandboxID)
	teamIDSanitized := sanitizeLokiLabel(teamID)

	// The stream selector is always built server-side and scopes the query to the
	// caller's own team + sandbox. Anything the client supplies is only ever appended
	// AFTER it; LogQL cannot reopen a stream selector in a later pipeline stage, so the
	// scoping holds no matter what the client passes.
	selector := fmt.Sprintf("{teamID=`%s`, sandboxID=`%s`, category!=\"metrics\"}", teamIDSanitized, sandboxIDSanitized)

	// When a raw pipeline is supplied the client owns the entire log pipeline; the
	// server contributes only the stream selector. The client provides its own parser
	// and filter stages in valid LogQL order, e.g. `| json | pid="1234"` or `|= "error"`.
	// This takes precedence over the structured level/search filters.
	if pipelineValue := strings.TrimSpace(utils.DerefOrDefault(pipeline, "")); pipelineValue != "" {
		return selector + " " + pipelineValue
	}

	if level == nil && utils.DerefOrDefault(search, "") == "" {
		return selector
	}

	query := selector + " | json"
	if level != nil {
		query += fmt.Sprintf(" | level =~ `%s`", minLevelRegexFilter(*level))
	}
	if utils.DerefOrDefault(search, "") != "" {
		query += fmt.Sprintf(" | message =~ `%s`", sanitizeLogMessageRegexFilter(*search))
	}

	return query
}
