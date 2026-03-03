package loki

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

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
