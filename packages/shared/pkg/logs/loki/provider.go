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
	"github.com/prometheus/prometheus/model/labels"
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

	// Only the client-supplied pipeline can be malformed; the stream selector and the
	// structured level/search filters are always built (and sanitized) server-side, so
	// we trust them by default. When a raw pipeline is supplied, validate the resulting
	// query locally before sending it to Loki: this returns a helpful parse error instead
	// of the opaque, retried failure the Loki client surfaces for a 400 response.
	if strings.TrimSpace(utils.DerefOrDefault(pipeline, "")) != "" {
		if err := validateScopedQuery(query, teamID, sandboxID); err != nil {
			return nil, err
		}
	}

	// Upstream Loki failures (transport errors, timeouts, mapping issues) stay
	// best-effort: log them and return an empty list so a transient outage does not
	// break the logs endpoint for otherwise-valid requests. Only a malformed query
	// (handled above) is surfaced as an error.
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

// validateScopedQuery parses a fully-built sandbox logs query and verifies that it
// is still scoped to the caller's own team + sandbox. It returns ErrInvalidQuery if
// the query does not parse, is not a log-selector query (e.g. a metric/binary
// expression), or no longer enforces the team/sandbox stream selector.
//
// The client only ever supplies a pipeline that is appended AFTER the server-built
// stream selector, so in normal use the leftmost selector — the one that decides
// which streams Loki reads — always carries the enforced matchers. This is a
// defense-in-depth check: rather than trusting that LogQL grammar guarantees the
// fragment can only ever filter (and never re-root the query into something like
// `selector or attacker_selector`), we assert the security property directly on the
// parsed AST, so scoping holds even if the query language evolves.
func validateScopedQuery(query string, teamID string, sandboxID string) error {
	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	}

	// A log query (returning streams) must be a LogSelectorExpr; metric/binary
	// expressions are not, so this also rejects attempts to change the query root.
	selector, ok := expr.(syntax.LogSelectorExpr)
	if !ok {
		return fmt.Errorf("%w: query must be a log selector query, not %T", ErrInvalidQuery, expr)
	}

	if !matchersEnforceScope(selector.Matchers(), sanitizeLokiLabel(teamID), sanitizeLokiLabel(sandboxID)) {
		return fmt.Errorf("%w: query must keep the enforced team and sandbox scope", ErrInvalidQuery)
	}

	return nil
}

// matchersEnforceScope reports whether the stream selector matchers contain exact
// equality matches for both the enforced team and sandbox.
func matchersEnforceScope(matchers []*labels.Matcher, teamID string, sandboxID string) bool {
	var hasTeam, hasSandbox bool
	for _, m := range matchers {
		if m.Type != labels.MatchEqual {
			continue
		}
		switch m.Name {
		case "teamID":
			hasTeam = m.Value == teamID
		case "sandboxID":
			hasSandbox = m.Value == sandboxID
		}
	}

	return hasTeam && hasSandbox
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
