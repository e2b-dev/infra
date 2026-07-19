package nodemanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type recordingInfoClient struct {
	orchestratorinfo.InfoServiceClient

	request *orchestratorinfo.ServiceStatusChangeRequest
}

func (c *recordingInfoClient) ServiceStatusOverrideFenced(_ context.Context, request *orchestratorinfo.ServiceStatusChangeRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	c.request = request

	return &emptypb.Empty{}, nil
}

func TestSendStatusChangeFencesStoredProcessIdentity(t *testing.T) {
	t.Parallel()

	info := &recordingInfoClient{}
	node := &Node{
		ID:     "node-1",
		meta:   NodeMetadata{ServiceInstanceID: "service-1"},
		client: &clusters.GRPCClient{Info: info},
	}

	require.NoError(t, node.SendStatusChange(t.Context(), api.NodeStatusDraining))
	require.Equal(t, "node-1", info.request.GetExpectedNodeId())
	require.Equal(t, "service-1", info.request.GetExpectedServiceId())
	require.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, info.request.GetServiceStatus())
}
