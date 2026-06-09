package loki

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

	query := buildSandboxLogsQuery("team`id", "sandbox`id", nil, nil)

	assert.Equal(t, "{teamID=`teamid`, sandboxID=`sandboxid`, category!=\"metrics\"}", query)
}

func TestBuildSandboxLogsQueryWithMessageSearch(t *testing.T) {
	t.Parallel()

	search := "hello` (world)+"
	query := buildSandboxLogsQuery("team-id", "sandbox-id", nil, &search)

	assert.Equal(
		t,
		"{teamID=`team-id`, sandboxID=`sandbox-id`, category!=\"metrics\"} | json | message =~ `.*hello \\(world\\)\\+.*`",
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

func TestBuildSandboxLogsQueryEscapesInjectionLikeSearchInput(t *testing.T) {
	t.Parallel()

	search := "`foo.*(bar)+?|baz\\qux` | json | level =~ `error`"
	query := buildSandboxLogsQuery("team-id", "sandbox-id", nil, &search)

	assert.Equal(
		t,
		"{teamID=`team-id`, sandboxID=`sandbox-id`, category!=\"metrics\"} | json | message =~ `.*foo\\.\\*\\(bar\\)\\+\\?\\|baz\\\\qux \\| json \\| level =~ error.*`",
		query,
	)
}
