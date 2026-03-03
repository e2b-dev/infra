package loki

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

