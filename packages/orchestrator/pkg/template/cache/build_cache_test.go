package cache

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildlogger"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func TestFailRunningFailsOnlyRunningBuilds(t *testing.T) {
	t.Parallel()

	buildCache := NewBuildCache(t.Context(), noop.NewMeterProvider())
	running, err := buildCache.Create("team-id", "running-build", buildlogger.NewLogEntryLogger())
	require.NoError(t, err)
	completed, err := buildCache.Create("team-id", "completed-build", buildlogger.NewLogEntryLogger())
	require.NoError(t, err)
	completed.SetSuccess(&templatemanager.TemplateBuildMetadata{})

	failed := buildCache.FailRunning(&templatemanager.TemplateBuildStatusReason{Message: "canceled"})
	require.Equal(t, 1, failed)
	require.Equal(t, templatemanager.TemplateBuildState_Failed, running.GetStatus())
	require.Equal(t, "canceled", running.GetResult().Reason.GetMessage())
	require.Equal(t, templatemanager.TemplateBuildState_Completed, completed.GetStatus())
}
