package nodemanager

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/shared/pkg/edge"
	grpcshared "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
)

func TestSandboxEventMetadataCtxReplacesCallerEventMetadata(t *testing.T) {
	t.Parallel()

	ctx := metadata.NewOutgoingContext(t.Context(), metadata.New(map[string]string{
		edge.EventTypeHeader:               "forged-event",
		edge.SandboxIDHeader:               "forged-sandbox",
		edge.SandboxTeamIDHeader:           "forged-team",
		edge.SandboxExecutionIDHeader:      "forged-execution",
		edge.SandboxOrchestratorIDHeader:   "forged-orchestrator",
		edge.SandboxOrchestratorIPHeader:   "192.0.2.99",
		edge.SandboxMaxLengthInHoursHeader: "999",
		edge.SandboxTrafficKeepaliveHeader: "false",
		grpcshared.IsResumeMetadataKey:     "true",
		"unrelated":                        "preserved",
	}))

	trusted := metadata.New(map[string]string{
		edge.EventTypeHeader:               edge.CatalogCreateEventType,
		edge.SandboxIDHeader:               "trusted-sandbox",
		edge.SandboxTeamIDHeader:           "trusted-team",
		edge.SandboxExecutionIDHeader:      "trusted-execution",
		edge.SandboxOrchestratorIDHeader:   "trusted-orchestrator",
		edge.SandboxOrchestratorIPHeader:   "10.0.0.7",
		edge.SandboxMaxLengthInHoursHeader: "24",
		edge.SandboxTrafficKeepaliveHeader: "true",
		grpcshared.IsResumeMetadataKey:     "false",
	})

	ctx = sandboxEventMetadataCtx(ctx, trusted)
	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)

	require.Equal(t, []string{edge.CatalogCreateEventType}, md.Get(edge.EventTypeHeader))
	require.Equal(t, []string{"trusted-sandbox"}, md.Get(edge.SandboxIDHeader))
	require.Equal(t, []string{"trusted-team"}, md.Get(edge.SandboxTeamIDHeader))
	require.Equal(t, []string{"trusted-execution"}, md.Get(edge.SandboxExecutionIDHeader))
	require.Equal(t, []string{"trusted-orchestrator"}, md.Get(edge.SandboxOrchestratorIDHeader))
	require.Equal(t, []string{"10.0.0.7"}, md.Get(edge.SandboxOrchestratorIPHeader))
	require.Equal(t, []string{"24"}, md.Get(edge.SandboxMaxLengthInHoursHeader))
	require.Equal(t, []string{"true"}, md.Get(edge.SandboxTrafficKeepaliveHeader))
	require.Equal(t, []string{"false"}, md.Get(grpcshared.IsResumeMetadataKey))
	require.Equal(t, []string{"preserved"}, md.Get("unrelated"))
}
