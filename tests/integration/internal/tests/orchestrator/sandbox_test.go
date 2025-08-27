package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestList(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetOrchestratorClient(t, ctx)

	list, err := client.List(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	assert.NotEmpty(t, list.GetSandboxes())
}
