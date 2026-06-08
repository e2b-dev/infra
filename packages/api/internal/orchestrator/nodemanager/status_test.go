package nodemanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type recordingInfoClient struct {
	orchestratorinfo.InfoServiceClient

	req *orchestratorinfo.ServiceStatusChangeRequest
}

func (r *recordingInfoClient) ServiceStatusOverride(_ context.Context, req *orchestratorinfo.ServiceStatusChangeRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	r.req = req

	return &emptypb.Empty{}, nil
}

func TestSendStatusChangeForwardsForceStop(t *testing.T) {
	t.Parallel()

	node := NewTestNode("node-id", api.NodeStatusReady, 0, 4)
	info := &recordingInfoClient{}
	node.client.Info = info

	err := node.SendStatusChange(t.Context(), api.NodeStatusDraining, true)
	require.NoError(t, err)
	require.NotNil(t, info.req)
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, info.req.GetServiceStatus())
	require.True(t, info.req.GetForceStop())
}
