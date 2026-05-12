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
		edge.SandboxExecutionIDHeader:      "forged-execution",
		edge.SandboxOrchestratorIDHeader:   "forged-orchestrator",
		edge.SandboxMaxLengthInHoursHeader: "999",
		grpcshared.IsResumeMetadataKey:     "true",
		"unrelated":                        "preserved",
	}))

	trusted := metadata.New(map[string]string{
		edge.EventTypeHeader:           edge.CatalogDeleteEventType,
		edge.SandboxIDHeader:           "trusted-sandbox",
		edge.SandboxExecutionIDHeader:  "trusted-execution",
		grpcshared.IsResumeMetadataKey: "false",
	})

	ctx = sandboxEventMetadataCtx(ctx, trusted)
	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)

	require.Equal(t, []string{edge.CatalogDeleteEventType}, md.Get(edge.EventTypeHeader))
	require.Equal(t, []string{"trusted-sandbox"}, md.Get(edge.SandboxIDHeader))
	require.Equal(t, []string{"trusted-execution"}, md.Get(edge.SandboxExecutionIDHeader))
	require.Empty(t, md.Get(edge.SandboxOrchestratorIDHeader))
	require.Empty(t, md.Get(edge.SandboxMaxLengthInHoursHeader))
	require.Equal(t, []string{"false"}, md.Get(grpcshared.IsResumeMetadataKey))
	require.Equal(t, []string{"preserved"}, md.Get("unrelated"))
}
