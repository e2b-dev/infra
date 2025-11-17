package ioc

import (
	"strings"
	"testing"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/stretchr/testify/require"
)

func TestAppGraph(t *testing.T) {
	t.Setenv("CONSUL_TOKEN", "consul-token")
	t.Setenv("NODE_ID", "testing-node-id")

	app := New(cfg.Config{}, "version", "commit-sha")
	require.NotNil(t, app)

	err := app.Start(t.Context())
	require.False(t, strings.HasPrefix(err.Error(), "fx.Provide"))
}
