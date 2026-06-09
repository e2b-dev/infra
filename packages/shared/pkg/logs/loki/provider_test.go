package loki

import (
	"context"
	"testing"
	"time"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

func TestSanitizeLokiLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "plain", input: "sandbox-id", expected: "sandbox-id"},
		{name: "removes_backticks", input: "`sandbox`id`", expected: "sandboxid"},
		{name: "empty", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, sanitizeLokiLabel(tt.input))
		})
	}
}

func TestSanitizeLogMessageRegexFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "plain", input: "hello", expected: ".*hello.*"},
		{name: "regex_meta", input: "a+b?(c)|d.*", expected: ".*a\\+b\\?\\(c\\)\\|d\\.\\*.*"},
		{name: "backslashes", input: "C:\\Users\\name", expected: ".*C:\\\\Users\\\\name.*"},
		{name: "removes_backticks", input: "`error`", expected: ".*error.*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, sanitizeLogMessageRegexFilter(tt.input))
		})
	}
}

func TestBuildSandboxLogsQueryWithoutSearch(t *testing.T) {
	t.Parallel()

	query := buildSandboxLogsQuery("team`id", "sandbox`id", nil, nil, nil)

	assert.Equal(t, "{teamID=`teamid`, sandboxID=`sandboxid`, category!=\"metrics\"}", query)
}

func TestBuildSandboxLogsQueryWithMessageSearch(t *testing.T) {
	t.Parallel()

	search := "hello` (world)+"
	query := buildSandboxLogsQuery("team-id", "sandbox-id", nil, &search, nil)

	assert.Equal(
		t,
		"{teamID=`team-id`, sandboxID=`sandbox-id`, category!=\"metrics\"} | json | message =~ `.*hello \\(world\\)\\+.*`",
		query,
	)
}

func TestBuildSandboxLogsQueryWithPipeline(t *testing.T) {
	t.Parallel()

	// The client owns the whole pipeline (including its own `| json`); the server only
	// contributes the enforced stream selector.
	pipeline := `| json | pid="1234" | event_type="process_output"`
	query := buildSandboxLogsQuery("team-id", "sandbox-id", nil, nil, &pipeline)

	assert.Equal(
		t,
		"{teamID=`team-id`, sandboxID=`sandbox-id`, category!=\"metrics\"} | json | pid=\"1234\" | event_type=\"process_output\"",
		query,
	)
}

func TestBuildSandboxLogsQueryWithLineFilterPipeline(t *testing.T) {
	t.Parallel()

	// A bare line filter is valid directly after the selector (no forced `| json` in
	// front of it that would produce invalid `| json |= "..."`).
	pipeline := `|= "error"`
	query := buildSandboxLogsQuery("team-id", "sandbox-id", nil, nil, &pipeline)

	assert.Equal(
		t,
		"{teamID=`team-id`, sandboxID=`sandbox-id`, category!=\"metrics\"} |= \"error\"",
		query,
	)
}

func TestBuildSandboxLogsQueryPipelineKeepsSelectorScope(t *testing.T) {
	t.Parallel()

	// Even an attempt to reference another team is only appended after the enforced
	// selector, so it can only narrow within the caller's own logs.
	pipeline := `| json | teamID="other-team"`
	query := buildSandboxLogsQuery("team-id", "sandbox-id", nil, nil, &pipeline)

	assert.Equal(
		t,
		"{teamID=`team-id`, sandboxID=`sandbox-id`, category!=\"metrics\"} | json | teamID=\"other-team\"",
		query,
	)
}

func TestBuildBuildLogsQuerySanitizesBackticks(t *testing.T) {
	t.Parallel()

	level := logs.LevelWarn
	query := buildBuildLogsQuery("env`id", "build`id", &level)

	assert.Equal(
		t,
		"{service=\"template-manager\", buildID=`buildid`, envID=`envid`} | json | level =~ `(warn|error)`",
		query,
	)
}

func TestBuildSandboxLogsQueryProducesValidLogQL(t *testing.T) {
	t.Parallel()

	level := logs.LevelWarn
	search := "needle (with) special+chars"
	pipeline := `| json | pid="1234" | event_type="process_output"`
	lineFilter := `|= "error"`

	tests := []struct {
		name     string
		level    *logs.LogLevel
		search   *string
		pipeline *string
	}{
		{name: "selector_only"},
		{name: "with_level", level: &level},
		{name: "with_search", search: &search},
		{name: "with_level_and_search", level: &level, search: &search},
		{name: "with_pipeline", pipeline: &pipeline},
		{name: "with_line_filter", pipeline: &lineFilter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			query := buildSandboxLogsQuery("team-id", "sandbox-id", tt.level, tt.search, tt.pipeline)

			// The server-built portions must always be valid LogQL so QuerySandboxLogs
			// never rejects them as ErrInvalidQuery (a false 400).
			_, err := syntax.ParseExpr(query)
			assert.NoErrorf(t, err, "expected valid LogQL, got query %q", query)
		})
	}
}

func TestQuerySandboxLogsRejectsInvalidQuery(t *testing.T) {
	t.Parallel()

	// An unterminated string literal in the client-supplied pipeline is invalid LogQL.
	pipeline := `| json | pid="unterminated`

	provider, err := NewLokiQueryProvider("http://loki.invalid", "", "")
	require.NoError(t, err)

	// Validation happens before any network call, so the nonexistent address is never
	// reached: the malformed query short-circuits with ErrInvalidQuery.
	res, err := provider.QuerySandboxLogs(
		context.Background(),
		"team-id",
		"sandbox-id",
		time.UnixMilli(0),
		time.UnixMilli(1),
		10,
		DefaultDirection,
		nil,
		nil,
		&pipeline,
	)

	assert.Nil(t, res)
	assert.ErrorIs(t, err, ErrInvalidQuery)
}

func TestQuerySandboxLogsRejectsScopeBypassPipeline(t *testing.T) {
	t.Parallel()

	// Each of these attempts to escape the enforced team/sandbox scoping by supplying
	// a binary/alternative selector or turning the query into a metric expression
	// instead of a plain filtering pipeline.
	bypassPipelines := []string{
		`or {category!="metrics"}`,
		`or {teamID="other-team"}`,
		`} or {teamID="other-team"}`,
		`| rate({teamID="other-team"}[5m])`,
	}

	provider, err := NewLokiQueryProvider("http://loki.invalid", "", "")
	require.NoError(t, err)

	for _, pipeline := range bypassPipelines {
		t.Run(pipeline, func(t *testing.T) {
			t.Parallel()

			res, qErr := provider.QuerySandboxLogs(
				context.Background(),
				"team-id",
				"sandbox-id",
				time.UnixMilli(0),
				time.UnixMilli(1),
				10,
				DefaultDirection,
				nil,
				nil,
				&pipeline,
			)

			assert.Nil(t, res)
			assert.ErrorIsf(t, qErr, ErrInvalidQuery, "scope-bypass pipeline %q must be rejected", pipeline)
		})
	}
}

func TestBuildSandboxLogsQueryEscapesInjectionLikeSearchInput(t *testing.T) {
	t.Parallel()

	search := "`foo.*(bar)+?|baz\\qux` | json | level =~ `error`"
	query := buildSandboxLogsQuery("team-id", "sandbox-id", nil, &search, nil)

	assert.Equal(
		t,
		"{teamID=`team-id`, sandboxID=`sandbox-id`, category!=\"metrics\"} | json | message =~ `.*foo\\.\\*\\(bar\\)\\+\\?\\|baz\\\\qux \\| json \\| level =~ error.*`",
		query,
	)
}
