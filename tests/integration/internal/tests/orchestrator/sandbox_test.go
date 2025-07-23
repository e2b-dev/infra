package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestList(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := setup.GetOrchestratorClient(t, ctx)

	list, err := client.List(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}

	assert.GreaterOrEqual(t, len(list.GetSandboxes()), 0)
}
